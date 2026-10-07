package queue

// MaxMessagesPerSendBatch mirrors the Cloudflare Queues limit: a sendBatch
// call carries at most 100 messages.
const MaxMessagesPerSendBatch = 100

// SendOptions optionally tunes one send. All fields optional.
type SendOptions struct {
	// ContentType tags the payload (e.g. "application/json").
	ContentType string
	// DelaySeconds defers delivery (0-7200, per the Queues limits).
	DelaySeconds int
}

// op is one queued send, recorded by Queue.Send into the invocation's
// outbox. No JS boundary crossing happens at Send time: the bridge drains
// the outbox after the handler returns and worker.js flushes it.
type op struct {
	queue        string
	body         []byte
	contentType  string
	delaySeconds int
}

// outbox accumulates sends for ONE invocation (one fetch or one batch).
// It is never a package global: workerd interleaves fetch and queue work in
// a single isolate, and a global outbox would cross-contaminate invocations.
// The bridge holds the current invocation's outbox, set by the consume and
// fetch entry points.
type outbox struct {
	ops []op
}

func (o *outbox) add(op op) { o.ops = append(o.ops, op) }

// flushPlan groups outbox ops by destination queue — buckets in first-seen
// order, each preserving registration order — and chunks each bucket into
// sendBatch-sized pieces. worker.js runs the buckets in parallel, so only
// per-queue order matters, and bucketing (no sort) keeps this allocation
// and dependency-light.
func (o *outbox) flushPlan() [][]op {
	if len(o.ops) == 0 {
		return nil
	}
	var order []string
	buckets := make(map[string][]op)
	for _, op := range o.ops {
		if _, seen := buckets[op.queue]; !seen {
			order = append(order, op.queue)
		}
		buckets[op.queue] = append(buckets[op.queue], op)
	}

	var chunks [][]op
	for _, name := range order {
		ops := buckets[name]
		for i := 0; i < len(ops); i += MaxMessagesPerSendBatch {
			end := i + MaxMessagesPerSendBatch
			if end > len(ops) {
				end = len(ops)
			}
			chunks = append(chunks, ops[i:end])
		}
	}
	return chunks
}

// Queue is a producer handle for one queue binding, obtained from the env
// via the bridge (EnvOf in runtime/cloudflare). Send appends to the
// current invocation's outbox; the runtime flushes it before the response
// returns (fetch) or the batch completes (queue), so a send failure fails
// the invocation and callers know the message was not enqueued.
type Queue struct {
	name string
}

// NewQueue builds a producer handle for a binding name. Handlers normally
// get handles from Env.GetQueue instead of calling this directly.
func NewQueue(name string) *Queue { return &Queue{name: name} }

// Name returns the binding name this handle was resolved from.
func (q *Queue) Name() string { return q.name }

// Send appends one message to the invocation outbox. Body is copied by
// reference into the outbox — handlers should not mutate it afterwards.
func (q *Queue) Send(body []byte) { q.send(body, SendOptions{}) }

// SendWithOptions appends one message with per-send options.
func (q *Queue) SendWithOptions(body []byte, opts SendOptions) { q.send(body, opts) }

func (q *Queue) send(body []byte, opts SendOptions) {
	currentOutbox.add(op{queue: q.name, body: body, contentType: opts.ContentType, delaySeconds: opts.DelaySeconds})
}

// SendBatch appends several messages to one queue, coalescing into
// sendBatch calls at flush time (chunked at MaxMessagesPerSendBatch).
func (q *Queue) SendBatch(bodies ...[]byte) {
	for _, b := range bodies {
		q.send(b, SendOptions{})
	}
}

// OutboxMessage is the wire shape of one outbox entry, as the bridge
// encodes it for worker.js to flush.
type OutboxMessage struct {
	Queue        string
	Body         []byte
	ContentType  string
	DelaySeconds int
}

// currentOutbox is the invocation-scoped outbox. The bridge resets it
// before dispatching into user code and drains it after; code outside the
// bridge never touches this. It is deliberately NOT a long-lived global
// accumulation: a fresh outbox per invocation is what keeps concurrent
// fetch and queue work in one isolate from cross-contaminating sends.
var currentOutbox = &outbox{}

// ResetOutbox discards any recorded sends. The bridge calls it on every
// invocation entry so a previous invocation's partial flush can never
// leak into the next one.
func ResetOutbox() { currentOutbox = &outbox{} }

// takeOutbox detaches the current outbox contents for flushing.
func takeOutbox() []op {
	ops := currentOutbox.ops
	currentOutbox = &outbox{}
	return ops
}

// PlanOutbox encodes the current outbox as the flush payload: sends
// coalesced per queue (registration order preserved within a queue) and
// chunked at MaxMessagesPerSendBatch. Exported for the bridge; user code
// does not drain the outbox itself.
func PlanOutbox() []OutboxMessage {
	ops := takeOutbox()
	chunks := (&outbox{ops: ops}).flushPlan()
	msgs := make([]OutboxMessage, 0, len(ops))
	for _, chunk := range chunks {
		for _, o := range chunk {
			msgs = append(msgs, OutboxMessage{Queue: o.queue, Body: o.body, ContentType: o.contentType, DelaySeconds: o.delaySeconds})
		}
	}
	return msgs
}
