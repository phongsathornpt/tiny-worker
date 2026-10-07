// Package queue adds Cloudflare Queues support to tiny-worker: a batch
// consumer wired through the runtime bridge, and an outbox-backed producer
// for sending messages from fetch handlers or other queue consumers.
//
// Delivery is at-least-once. A consumer that dies mid-batch (including a
// trap panic under TinyGo) sees the unacked tail redelivered; messages the
// runtime confirmed before the death stay acked. Message.ID and Attempts
// are the tools for dedupe and poison-message skip logic.
//
// The consumer contract is fail-fast: the first message whose handling
// fails stops the batch. Messages after it stay unacked and are redelivered
// in order, so a poison message delays the messages behind it (bounded by
// the queue's max_retries and DLQ configuration in wrangler.jsonc). Use
// msg.Attempts inside the handler to park poison messages instead.
//
// Like the rest package, queue is opt-in by import: workers that never
// touch it pay nothing, and the JSON decode helper lives in a separate
// package (queuejson) so importing queue alone keeps encoding/json out of
// the wasm.
package queue

import (
	"strconv"
	"time"
)

// notAcked is the initial value of Batch.ackedThrough: nothing recorded.
const notAcked = -1

// Message is one queue message. Body is the raw bytes the producer sent;
// use the queuejson package to decode structured payloads.
type Message struct {
	// ID is the producer-assigned message id, unique per send. Redeliveries
	// keep the same ID, so it keys idempotency checks.
	ID string
	// Body is the message payload as delivered. Never nil.
	Body []byte
	// ContentType is the producer-provided content type, if any.
	ContentType string
	// TimestampMS is the enqueue time as unix milliseconds (0 when absent).
	TimestampMS int64
	// Attempts counts deliveries including this one: 1 on first delivery,
	// 2 on the first redelivery, and so on.
	Attempts int
}

// RetryError marks a message for redelivery. Return one from a handler
// (typically via msg.Retry) to stop the batch: this message and the ones
// behind it stay unacked and are redelivered in order, after the optional
// delay.
type RetryError struct {
	// DelaySeconds asks the queue to wait before redelivering. The runtime
	// enforces the queue's configured limits (0-7200).
	DelaySeconds int
	// Err is the underlying failure, for logs and errors.Is checks.
	Err error
}

func (e *RetryError) Error() string {
	msg := "queue: retry requested"
	if e.Err != nil {
		msg = e.Err.Error()
	}
	if e.DelaySeconds > 0 {
		return msg + " (retry after " + strconv.Itoa(e.DelaySeconds) + "s)"
	}
	return msg
}

func (e *RetryError) Unwrap() error { return e.Err }

// Retry asks for this message to be redelivered after an optional delay,
// stopping the batch so later messages are not processed out of order.
func (m *Message) Retry(delaySeconds int) *RetryError {
	return &RetryError{DelaySeconds: delaySeconds}
}

// Batch is one delivery of messages to a consumer. Handlers walk Messages
// in order. Progress is recorded with AckThrough so a mid-batch failure
// keeps the messages before the failure acked; Handle maintains it
// automatically.
type Batch struct {
	Messages []*Message
	// Queue is the binding name the batch arrived on (informational).
	Queue string

	// ackedThrough is the highest index durably processed (notAcked while
	// nothing is). Read it through watermark(), which interprets the
	// sentinel differently on the success and failure paths. ackFn is set
	// by the bridge so every AckThrough is mirrored to JS as it happens —
	// that mirror is what lets worker.js keep acked messages acked across
	// a mid-batch trap panic.
	ackedThrough int
	ackFn        func(index int)
}

// newBatch builds a batch with fresh bookkeeping.
func newBatch(name string, msgs []*Message) *Batch {
	return &Batch{Messages: msgs, Queue: name, ackedThrough: notAcked}
}

// NewBatch builds a batch from delivered messages. The bridge uses this
// when decoding the runtime payload; tests and custom dispatchers may too.
func NewBatch(name string, msgs []*Message) *Batch { return newBatch(name, msgs) }

// AckThrough records that messages 0..index are durably processed. A batch
// handler that fails midway calls this before returning the error, so the
// messages before the failure stay acked and only the tail redelivers.
// Callers must not go backwards: later calls with a lower index are
// ignored, matching the monotonic cumulative watermark the bridge sends.
//
// When the bridge attached a JS ack mirror, each advance also calls it
// with the new watermark — a synchronous, allocation-free notification so
// worker.js can record progress while the batch is still running (and
// still alive).
func (b *Batch) AckThrough(index int) {
	if index > b.ackedThrough {
		b.ackedThrough = index
		if b.ackFn != nil {
			b.ackFn(index)
		}
	}
}

// OnAck registers fn to be invoked synchronously on every watermark
// advance, receiving the new cumulative count. The bridge uses this to
// mirror acks to worker.js as they happen; custom dispatchers may use it
// for the same purpose.
func (b *Batch) OnAck(fn func(index int)) { b.ackFn = fn }

// watermark is the cumulative ack count the bridge forwards to worker.js:
// how many messages (from the front) are safe to ack. On success that is
// everything. On failure it is only what the handler explicitly recorded
// with AckThrough before failing — nothing, if it failed at index 0
// without recording — so a failed message is never silently acked.
func (b *Batch) watermark(failed bool) int {
	if !failed {
		return len(b.Messages)
	}
	if b.ackedThrough == notAcked {
		return 0
	}
	return b.ackedThrough + 1
}

// AckedCount reports how many messages (from the front) have been
// explicitly recorded as processed via AckThrough. It never counts
// unrecorded messages, so it reads honestly mid-batch and after a failure;
// Consumer.Serve computes the outcome-aware watermark for acking.
func (b *Batch) AckedCount() int {
	if b.ackedThrough == notAcked {
		return 0
	}
	return b.ackedThrough + 1
}

// Handler processes one batch. Return nil to ack the batch (or everything
// recorded with AckThrough); return any error — typically a *RetryError
// from msg.Retry — to stop at the failure point and redeliver the tail.
type Handler func(*Batch) error

// Middleware wraps a consumer handler: it sees every batch, the outcome
// (via the returned error), the queue name, and duration.
type Middleware func(Handler) Handler

// Consumer is a configured queue consumer: a batch handler wrapped in
// middleware. Build one with New and hand it to RegisterQueue.
type Consumer struct {
	handler    Handler
	middleware []Middleware
	queueName  string
}

// Options configures a Consumer. All fields are optional.
type Options struct {
	// Queue names the binding (for logs and error messages only).
	Queue string
}

// New builds a consumer around handler. Options may be nil.
func New(handler Handler, opts *Options) *Consumer {
	c := &Consumer{handler: handler}
	if opts != nil {
		c.queueName = opts.Queue
	}
	return c
}

// Use appends consumer middleware. The first registered middleware is the
// outermost, matching App.Use.
func (c *Consumer) Use(m ...Middleware) { c.middleware = append(c.middleware, m...) }

// Name returns the configured queue binding name ("" when unset).
func (c *Consumer) Name() string { return c.queueName }

// Serve runs the middleware chain around the user handler and converts the
// outcome into a cumulative watermark plus retry delay. The bridge calls
// this for each delivered batch and forwards both to worker.js, which
// records the watermark so a mid-batch runtime death redelivers only the
// unacked tail.
func (c *Consumer) Serve(b *Batch) (watermark int, delay int, err error) {
	err = c.chain()(b)
	if err == nil {
		return b.watermark(false), 0, nil
	}
	var re *RetryError
	if asRetry(err, &re) {
		return b.watermark(true), re.DelaySeconds, err
	}
	// Unknown error shape: still fail-fast, message preserved for logs.
	return b.watermark(true), 0, &RetryError{Err: err}
}

// chain composes middleware around the handler in reverse registration
// order (first registered is outermost), like App.Use.
func (c *Consumer) chain() Handler {
	h := c.handler
	for i := len(c.middleware) - 1; i >= 0; i-- {
		h = c.middleware[i](h)
	}
	return h
}

// Handle converts a per-message function into a batch Handler with
// fail-fast semantics and automatic progress marking: the first error
// stops the batch, and every message before it stays acked. This is sugar
// over the batch primitive — handlers never see batching mechanics.
func Handle(fn func(*Message) error) Handler {
	return func(b *Batch) error {
		for i, m := range b.Messages {
			if err := fn(m); err != nil {
				var re *RetryError
				if asRetry(err, &re) {
					return re
				}
				return &RetryError{Err: err}
			}
			b.AckThrough(i)
		}
		return nil
	}
}

// BatchResult is the per-batch outcome middleware observes.
type BatchResult struct {
	Queue     string
	Total     int
	Acked     int
	Retried   bool
	DelaySecs int
	Err       error
	Duration  time.Duration
}

// LogBatchesFunc receives one line per batch. Log to whatever sink you
// have; println reaches the worker console.
type LogBatchesFunc func(res BatchResult)

// LogBatches logs one line per delivered batch (not per message) with the
// queue name, acked/total counts, and duration — the queue analog of
// LogRequests. Failures additionally carry the retry delay.
func LogBatches(log LogBatchesFunc) Middleware {
	return func(next Handler) Handler {
		return func(b *Batch) error {
			start := time.Now()
			err := next(b)
			res := BatchResult{
				Queue:    b.Queue,
				Total:    len(b.Messages),
				Acked:    b.watermark(err != nil),
				Duration: time.Since(start),
				Err:      err,
			}
			if err != nil {
				var re *RetryError
				if asRetry(err, &re) {
					res.Retried = true
					res.DelaySecs = re.DelaySeconds
				}
			}
			log(res)
			return err
		}
	}
}

// asRetry walks the Unwrap chain looking for a *RetryError, so wrapped
// errors (fmt.Errorf("%w")) still count as retry requests.
func asRetry(err error, target **RetryError) bool {
	for err != nil {
		if re, ok := err.(*RetryError); ok {
			*target = re
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}
