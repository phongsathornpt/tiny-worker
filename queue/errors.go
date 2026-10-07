package queue

import "errors"

// ErrSendFailed is wrapped by every *QueueError, so callers can
// errors.Is(err, queue.ErrSendFailed) without caring about the binding.
var ErrSendFailed = errors.New("queue send failed")

// QueueError reports a producer send failure at flush time: the queue
// binding rejected a send or sendBatch. The runtime fails the invocation
// (500 on fetch; rethrown batch on queue), so callers know the message was
// NOT enqueued. Err carries the runtime's error text.
type QueueError struct {
	// Queue is the binding name the send targeted.
	Queue string
	// Err is the underlying error text from the runtime.
	Err string
}

func (e *QueueError) Error() string { return "queue: send to " + e.Queue + " failed: " + e.Err }

func (e *QueueError) Unwrap() error { return ErrSendFailed }
