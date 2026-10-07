// Package queuejson adds opt-in JSON decoding to queue messages. Importing
// it is what links encoding/json into a queue worker — consumers of raw
// bytes never pay for it.
//
//	c := queue.New(queuejson.HandleBatchDecode(process), nil)
//
//	func process(b *queue.Batch, v Payload) error { ... }
package queuejson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/phongsathornpt/tiny-worker/queue"
)

// Decode unmarshals a message body as JSON into v. Trailing non-whitespace
// after the first JSON value is rejected so malformed frames fail loudly.
func Decode(m *queue.Message, v any) error {
	d := json.NewDecoder(bytes.NewReader(m.Body))
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("queuejson: decode message %q: %w", m.ID, err)
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("queuejson: message %q: trailing data after JSON value", m.ID)
	}
	return nil
}

// Handler processes one batch whose messages all decode to T. Per-message
// decode failures stop the batch as a retry, so a malformed message
// redelivers (and reaches the DLQ per the queue's wrangler config) instead
// of vanishing silently.
type Handler[T any] func(b *queue.Batch, value T) error

// HandleBatchDecode converts a typed handler into a queue.Handler that
// decodes every message body as JSON of type T before invoking fn. The
// handler receives the batch plus each decoded value, so batch-level
// decisions stay available.
func HandleBatchDecode[T any](fn Handler[T]) queue.Handler {
	return func(b *queue.Batch) error {
		for _, m := range b.Messages {
			var v T
			if err := Decode(m, &v); err != nil {
				return m.Retry(0)
			}
			if err := fn(b, v); err != nil {
				return err
			}
			b.AckThrough(indexOf(b, m))
		}
		return nil
	}
}

// indexOf returns m's position in b, or -1 when absent (defensive: keeps
// progress marking correct even if a handler reordered b.Messages).
func indexOf(b *queue.Batch, m *queue.Message) int {
	for i, cand := range b.Messages {
		if cand == m {
			return i
		}
	}
	return -1
}
