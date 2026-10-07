package queue

import (
	"errors"
	"testing"
)

func TestHandleProcessesAllAndAcksAll(t *testing.T) {
	var seen []string
	h := Handle(func(m *Message) error {
		seen = append(seen, m.ID)
		return nil
	})
	b := newBatch("q", []*Message{{ID: "a"}, {ID: "b"}, {ID: "c"}})
	if err := h(b); err != nil {
		t.Fatalf("handler returned %v, want nil", err)
	}
	if len(seen) != 3 || seen[0] != "a" || seen[2] != "c" {
		t.Fatalf("seen = %v", seen)
	}
	if got := b.watermark(false); got != 3 {
		t.Fatalf("watermark = %d, want 3", got)
	}
}

func TestHandleFailFastStopsAndAcksPrefix(t *testing.T) {
	count := 0
	h := Handle(func(m *Message) error {
		count++
		if m.ID == "b" {
			return m.Retry(30)
		}
		return nil
	})
	b := newBatch("q", []*Message{{ID: "a"}, {ID: "b"}, {ID: "c"}})
	err := h(b)
	var re *RetryError
	if !asRetry(err, &re) {
		t.Fatalf("err = %v, want *RetryError", err)
	}
	if re.DelaySeconds != 30 {
		t.Fatalf("DelaySeconds = %d, want 30", re.DelaySeconds)
	}
	if count != 2 {
		t.Fatalf("processed %d messages, want 2 (fail-fast stops at b)", count)
	}
	if got := b.watermark(true); got != 1 {
		t.Fatalf("watermark = %d, want 1 (only a acked)", got)
	}
}

func TestHandleWrapsPlainErrorsAsRetry(t *testing.T) {
	boom := errors.New("boom")
	h := Handle(func(*Message) error { return boom })
	b := newBatch("q", []*Message{{ID: "a"}})
	err := h(b)
	var re *RetryError
	if !asRetry(err, &re) || re.Err != boom {
		t.Fatalf("err = %v, want wrapped boom", err)
	}
}

func TestBatchAckThroughMonotonic(t *testing.T) {
	b := newBatch("q", []*Message{{ID: "a"}, {ID: "b"}})
	b.AckThrough(0)
	if got := b.AckedCount(); got != 1 {
		t.Fatalf("after AckThrough(0): %d, want 1", got)
	}
	b.AckThrough(-1) // backwards: ignored
	if got := b.AckedCount(); got != 1 {
		t.Fatalf("backwards AckThrough changed watermark: %d, want 1", got)
	}
	b.AckThrough(1)
	if got := b.AckedCount(); got != 2 {
		t.Fatalf("after AckThrough(1): %d, want 2", got)
	}
}

func TestServeWatermarkAndDelay(t *testing.T) {
	t.Run("all acked", func(t *testing.T) {
		c := New(Handle(func(*Message) error { return nil }), &Options{Queue: "orders"})
		if c.Name() != "orders" {
			t.Fatalf("Name = %q", c.Name())
		}
		wm, delay, err := c.Serve(newBatch("orders", []*Message{{ID: "a"}, {ID: "b"}}))
		if err != nil || wm != 2 || delay != 0 {
			t.Fatalf("serve = (%d,%d,%v)", wm, delay, err)
		}
	})
	t.Run("fail fast with delay", func(t *testing.T) {
		c := New(Handle(func(m *Message) error {
			if m.ID == "b" {
				return m.Retry(60)
			}
			return nil
		}), nil)
		wm, delay, err := c.Serve(newBatch("q", []*Message{{ID: "a"}, {ID: "b"}, {ID: "c"}}))
		if err == nil || wm != 1 || delay != 60 {
			t.Fatalf("serve = (%d,%d,%v), want (1,60,err)", wm, delay, err)
		}
	})
	t.Run("unknown error shape still fail-fast", func(t *testing.T) {
		c := New(Handle(func(m *Message) error {
			if m.ID == "a" {
				return errors.New("boom")
			}
			return nil
		}), nil)
		wm, delay, err := c.Serve(newBatch("q", []*Message{{ID: "a"}}))
		if err == nil || wm != 0 || delay != 0 {
			t.Fatalf("serve = (%d,%d,%v), want (0,0,err)", wm, delay, err)
		}
	})
}

func TestConsumerMiddlewareChain(t *testing.T) {
	var order []string
	c := New(Handle(func(*Message) error { return nil }), nil)
	c.Use(
		func(next Handler) Handler {
			return func(b *Batch) error {
				order = append(order, "outer-in")
				err := next(b)
				order = append(order, "outer-out")
				return err
			}
		},
		func(next Handler) Handler {
			return func(b *Batch) error {
				order = append(order, "inner-in")
				return next(b)
			}
		},
	)
	if _, _, err := c.Serve(newBatch("q", []*Message{{ID: "a"}})); err != nil {
		t.Fatal(err)
	}
	want := []string{"outer-in", "inner-in", "outer-out"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestLogBatchesMiddleware(t *testing.T) {
	var got BatchResult
	c := New(Handle(func(m *Message) error {
		if m.ID == "b" {
			return m.Retry(45)
		}
		return nil
	}), &Options{Queue: "jobs"})
	c.Use(LogBatches(func(res BatchResult) { got = res }))
	wm, delay, err := c.Serve(newBatch("jobs", []*Message{{ID: "a"}, {ID: "b"}, {ID: "c"}}))
	if err == nil || wm != 1 || delay != 45 {
		t.Fatalf("serve = (%d,%d,%v)", wm, delay, err)
	}
	if got.Queue != "jobs" || got.Total != 3 || got.Acked != 1 || !got.Retried || got.DelaySecs != 45 {
		t.Fatalf("LogBatches saw %+v", got)
	}
}

func TestOutboxCoalescesByQueueAndChunks(t *testing.T) {
	a, b := &Queue{name: "a"}, &Queue{name: "b"}
	a.Send([]byte("a1"))
	b.Send([]byte("b1"))
	a.Send([]byte("a2"))
	for i := 0; i < MaxMessagesPerSendBatch+5; i++ {
		a.Send([]byte("filler"))
	}

	msgs := PlanOutbox()
	want := MaxMessagesPerSendBatch + 8 // a1, b1, a2 + 105 fillers
	if len(msgs) != want {
		t.Fatalf("planned %d messages, want %d", len(msgs), want)
	}
	// All "a" messages contiguous and first (sorted by queue name), order kept.
	if msgs[0].Queue != "a" || msgs[0].Body == nil || string(msgs[0].Body) != "a1" {
		t.Fatalf("first outbox message = %+v, want a1 on queue a", msgs[0])
	}
	if string(msgs[1].Body) != "a2" {
		t.Fatalf("per-queue order broken: %+v", msgs[1])
	}
	// Chunk boundaries: exactly one group of b somewhere after the a-fillers.
	sawB := false
	for i, m := range msgs {
		if m.Queue == "b" {
			if sawB {
				t.Fatalf("queue b group split at %d", i)
			}
			sawB = true
			continue
		}
		if sawB {
			t.Fatalf("queue a message after b group at %d", i)
		}
	}
	if !sawB {
		t.Fatal("queue b messages lost")
	}

	// Outbox is detached after planOutbox.
	if extra := PlanOutbox(); len(extra) != 0 {
		t.Fatalf("outbox not drained: %d leftover", len(extra))
	}
}

func TestSendWithOptions(t *testing.T) {
	q := &Queue{name: "x"}
	q.SendWithOptions([]byte("p"), SendOptions{ContentType: "application/json", DelaySeconds: 120})
	msgs := PlanOutbox()
	if len(msgs) != 1 || msgs[0].ContentType != "application/json" || msgs[0].DelaySeconds != 120 {
		t.Fatalf("outbox = %+v", msgs)
	}
}

func TestQueueErrorUnwraps(t *testing.T) {
	err := &QueueError{Queue: "orders", Err: "quota exceeded"}
	if !errors.Is(err, ErrSendFailed) {
		t.Fatal("QueueError must unwrap to ErrSendFailed")
	}
	if err.Error() != "queue: send to orders failed: quota exceeded" {
		t.Fatalf("Error() = %q", err.Error())
	}
}

func TestRetryErrorRendering(t *testing.T) {
	if got := (&RetryError{DelaySeconds: 10}).Error(); got != "queue: retry requested (retry after 10s)" {
		t.Fatalf("Error() = %q", got)
	}
	if got := (&RetryError{Err: errors.New("db down")}).Error(); got != "db down" {
		t.Fatalf("Error() = %q, want underlying text", got)
	}
}
