//go:build js && wasm

// Package queuebridge holds the Cloudflare Queues side of the runtime
// bridge: consume (batches into Go), flush (producer sends back out), and
// per-invocation env resolution. It is a separate package from
// runtime/cloudflare so fetch-only workers never link queue plumbing — the
// same opt-in-by-import contract as the rest package, enforced by a
// dedicated size baseline.
package queuebridge

import (
	"syscall/js"

	tinyworker "github.com/phongsathornpt/tiny-worker"
	"github.com/phongsathornpt/tiny-worker/queue"
)

// Queue support: worker.js delivers batches via tinyWorkerQueueConsume and
// drains producer sends via tinyWorkerQueueFlush. Ack progress flows back
// as a cumulative watermark on the consume result; a queue-path runtime
// death loses only the messages above the watermark, which worker.js
// redelivers by rethrowing (Cloudflare retries the batch).
//
// Producer sends never touch JS at Send time: they append to the
// invocation outbox, and worker.js flushes it after the handler returns —
// awaiting one sendBatch per queue chunk before completing the invocation.
// Go records binding NAMES only; worker.js resolves each name against its
// own invocation env, so nothing JS-shaped is ever cached across
// invocations.

var (
	queueConsumer *queue.Consumer
	queueFunc     js.Func
	flushFunc     js.Func
)

// RegisterQueue attaches a queue consumer to the bridge, next to the fetch
// app. Multiple calls replace the consumer; one worker binds one consumer
// queue (fan-out belongs in the handler).
func RegisterQueue(c *queue.Consumer) {
	queueConsumer = c
	if queueFunc.Value.IsUndefined() {
		queueFunc = js.FuncOf(consume)
		js.Global().Set("tinyWorkerQueueConsume", queueFunc)
	}
	if flushFunc.Value.IsUndefined() {
		flushFunc = js.FuncOf(flushOutbox)
		js.Global().Set("tinyWorkerQueueFlush", flushFunc)
	}
}

// currentEnv is the env of the invocation in flight. The bridge is
// synchronous — a Go call runs to completion before the next payload
// enters — so a single slot is race-free; it is set on entry and cleared
// on exit by both paths (fetch and queue).
var currentEnv *Env

// CurrentEnv returns the Worker env for the invocation in flight: the same
// value EnvOf yields on the fetch path, available to queue handlers for
// chained sends. It returns nil outside handler execution.
func CurrentEnv() *Env { return currentEnv }

// consume is the queue path: decode the batch payload, dispatch through
// the consumer, return the cumulative ack watermark and retry delay.
//
// Acks cross the boundary DURING the batch: every AckThrough from user
// code synchronously calls the JS ack callback registered here (one int,
// zero allocation), so worker.js's ledger stays current even if a later
// message traps the runtime. The final watermark on the result covers
// handlers that fail without recording; the retry delay rides along for
// worker.js to honor when rethrowing.
func consume(_ js.Value, args []js.Value) any {
	result := js.Global().Get("Object").New()
	if queueConsumer == nil {
		result.Set("error", "tiny-worker: no queue consumer registered")
		return result
	}
	if len(args) < 1 {
		result.Set("error", "tiny-worker: queue payload missing")
		return result
	}
	payload := args[0]

	b, env := batchFromJS(payload)
	b.OnAck(func(index int) {
		// One int per advance, count-shaped (how many are acked), so the
		// JS ledger is a single number that survives a runtime death.
		js.Global().Call("tinyWorkerQueueAck", index+1)
	})
	queue.ResetOutbox() // never inherit a previous invocation's sends
	currentEnv = env
	watermark, delay, err := queueConsumer.Serve(b)
	currentEnv = nil
	// Chained sends flush before the batch result returns: worker.js
	// awaits them and rethrows on failure, so acked messages stay acked
	// and the tail redelivers with the sends reapplied.
	result.Set("outbox", outboxToJS())

	result.Set("watermark", watermark)
	result.Set("delay", delay)
	if err != nil {
		result.Set("error", err.Error())
	}
	return result
}

// flushOutbox encodes the producer outbox for the current fetch invocation:
// worker.js calls it after the Go handler returns, awaits one sendBatch per
// queue (coalesced, chunked at queue.MaxMessagesPerSendBatch), and fails
// the request when any send fails — so callers learn the message was not
// enqueued.
func flushOutbox(_ js.Value, _ []js.Value) any {
	return outboxToJS()
}

// outboxToJS encodes the current outbox as
// { count, queues: { name: [{ body, contentType?, delaySeconds? }] } }.
func outboxToJS() js.Value {
	ops := queue.PlanOutbox()
	out := js.Global().Get("Object").New()
	out.Set("count", len(ops))
	queues := js.Global().Get("Object").New()
	for _, op := range ops {
		arr := queues.Get(op.Queue)
		if arr.IsUndefined() {
			arr = js.Global().Get("Array").New()
			queues.Set(op.Queue, arr)
		}
		msg := js.Global().Get("Object").New()
		body := js.Global().Get("Uint8Array").New(len(op.Body))
		js.CopyBytesToJS(body, op.Body)
		msg.Set("body", body)
		if op.ContentType != "" {
			msg.Set("contentType", op.ContentType)
		}
		if op.DelaySeconds > 0 {
			msg.Set("delaySeconds", op.DelaySeconds)
		}
		arr.Call("push", msg)
	}
	out.Set("queues", queues)
	return out
}

// batchFromJS decodes worker.js's queue payload into a *queue.Batch plus
// the invocation env: { queue, env, messages: [{ id, body, contentType,
// timestamp, attempts }] }. Bodies arrive as Uint8Array and are copied into
// Go memory exactly like fetch bodies.
func batchFromJS(payload js.Value) (*queue.Batch, *Env) {
	msgsArr := payload.Get("messages")
	count := msgsArr.Length()
	msgs := make([]*queue.Message, 0, count)
	for i := 0; i < count; i++ {
		m := msgsArr.Index(i)
		msg := &queue.Message{
			ID:       m.Get("id").String(),
			Attempts: m.Get("attempts").Int(),
		}
		if ct := m.Get("contentType"); !ct.IsUndefined() && !ct.IsNull() {
			msg.ContentType = ct.String()
		}
		if ts := m.Get("timestamp"); ts.Type() == js.TypeNumber {
			msg.TimestampMS = int64(ts.Float())
		}
		if body := m.Get("body"); !body.IsUndefined() && !body.IsNull() {
			buf := make([]byte, body.Get("byteLength").Int())
			js.CopyBytesToGo(buf, body)
			msg.Body = buf
		} else {
			msg.Body = []byte{}
		}
		msgs = append(msgs, msg)
	}

	name := ""
	if q := payload.Get("queue"); !q.IsUndefined() && !q.IsNull() {
		name = q.String()
	}
	return queue.NewBatch(name, msgs), envFromJS(payload.Get("env"))
}

// Env carries the Worker's bindings into Go. Fetch handlers reach it with
// EnvOf(req); queue handlers with CurrentEnv().
type Env struct {
	obj js.Value
}

// GetQueue resolves a queue binding by its wrangler.jsonc name and returns
// a producer handle whose sends join the invocation outbox (flushed by
// worker.js after the handler returns). A missing binding errors
// immediately instead of failing later at flush time.
func (e *Env) GetQueue(name string) (*queue.Queue, error) {
	if e == nil || e.obj.IsUndefined() {
		// Outside workerd (native tests): a detached handle whose sends
		// stay in the outbox. Keeps handlers testable without JS.
		return queue.NewQueue(name), nil
	}
	binding := e.obj.Get(name)
	if binding.IsUndefined() || binding.IsNull() {
		return nil, &queue.QueueError{Queue: name, Err: "binding not present in env (check wrangler.jsonc)"}
	}
	return queue.NewQueue(name), nil
}

// envFromJS wraps the payload's env object (nil when absent).
func envFromJS(v js.Value) *Env {
	if v.IsUndefined() || v.IsNull() {
		return nil
	}
	return &Env{obj: v}
}

// EnvOf returns the Worker env attached to a fetch request by the runtime
// bridge, or nil when the request carries none (queue-free workers, or a
// handler reached outside workerd). Handlers use it to resolve producer
// bindings:
//
//	env := queuebridge.EnvOf(req)
//	q, err := env.GetQueue("MY_QUEUE")
//	q.Send(body)
func EnvOf(req *tinyworker.Request) *Env {
	if req == nil {
		return nil
	}
	if v, ok := req.Get(envKey); ok {
		if raw, ok := v.(js.Value); ok {
			return envFromJS(raw)
		}
	}
	return nil
} // envKey mirrors runtime/cloudflare's envStoreKey (duplicated on purpose:
// the fetch bridge must not import this package, and this package reads
// only the value it stores).
const envKey = "cloudflare.env"
