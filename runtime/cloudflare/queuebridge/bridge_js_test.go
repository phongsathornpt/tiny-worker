//go:build js && wasm

package queuebridge

import (
	"errors"
	"strings"
	"syscall/js"
	"testing"

	tinyworker "github.com/phongsathornpt/tiny-worker"
	"github.com/phongsathornpt/tiny-worker/queue"
)

// queueMsg is the test-side shape of one delivered message.
type queueMsg struct {
	id          string
	body        []byte
	contentType string
	attempts    int
}

// queuePayload builds a JS object shaped like worker.js's queue payload:
// { queue, env, invocationId, messages: [{ id, body, contentType, attempts }] }.
func queuePayload(t *testing.T, name string, env js.Value, msgs ...queueMsg) js.Value {
	t.Helper()
	obj := js.Global().Get("Object").New()
	obj.Set("queue", name)
	if !env.IsUndefined() {
		obj.Set("env", env)
	}
	obj.Set("invocationId", "q-test")
	arr := js.Global().Get("Array").New(len(msgs))
	for i, m := range msgs {
		o := js.Global().Get("Object").New()
		o.Set("id", m.id)
		body := js.Global().Get("Uint8Array").New(len(m.body))
		if len(m.body) > 0 {
			js.CopyBytesToJS(body, m.body)
		}
		o.Set("body", body)
		if m.contentType != "" {
			o.Set("contentType", m.contentType)
		}
		o.Set("attempts", m.attempts)
		arr.SetIndex(i, o)
	}
	obj.Set("messages", arr)
	return obj
}

// callConsume invokes the bridge's consume entry point and decodes the
// { watermark, delay, error?, outbox? } result.
func callConsume(t *testing.T, payload js.Value) (watermark, delay int, errMsg string, outbox js.Value) {
	t.Helper()
	res, ok := consume(js.Undefined(), []js.Value{payload}).(js.Value)
	if !ok {
		t.Fatal("consume returned no js.Value")
	}
	watermark = res.Get("watermark").Int()
	delay = res.Get("delay").Int()
	if e := res.Get("error"); !e.IsUndefined() {
		errMsg = e.String()
	}
	outbox = res.Get("outbox")
	return watermark, delay, errMsg, outbox
}

// buildQueueConsumer registers a consumer for the duration of one test,
// cleaning up the JS globals the bridge publishes.
func buildQueueConsumer(t *testing.T, c *queue.Consumer) {
	t.Helper()
	RegisterQueue(c)
	t.Cleanup(func() {
		queueConsumer = nil
		if queueFunc.Value.Get("id").Truthy() {
			queueFunc.Release()
			queueFunc = js.FuncOf(nil)
		}
		if flushFunc.Value.Get("id").Truthy() {
			flushFunc.Release()
			flushFunc = js.FuncOf(nil)
		}
		js.Global().Delete("tinyWorkerQueueConsume")
		js.Global().Delete("tinyWorkerQueueFlush")
	})
}

func TestQueueConsumeDecodesBatchAndAcksAll(t *testing.T) {
	var got *queue.Batch
	buildQueueConsumer(t, queue.New(func(b *queue.Batch) error {
		got = b
		return nil
	}, &queue.Options{Queue: "orders"}))

	env := js.Global().Get("Object").New()
	env.Set("ORDERS", js.Global().Get("Object").New())

	wm, delay, errMsg, _ := callConsume(t, queuePayload(t, "orders", env,
		queueMsg{id: "m1", body: []byte("one")},
		queueMsg{id: "m2", body: []byte("two"), attempts: 2},
	))
	if errMsg != "" {
		t.Fatalf("consume error: %s", errMsg)
	}
	if wm != 2 || delay != 0 {
		t.Fatalf("watermark=%d delay=%d, want 2/0", wm, delay)
	}
	if got == nil || len(got.Messages) != 2 || got.Messages[1].ID != "m2" || got.Messages[1].Attempts != 2 {
		t.Fatalf("batch decoded wrong: %+v", got)
	}
	if string(got.Messages[0].Body) != "one" {
		t.Fatalf("body = %q", got.Messages[0].Body)
	}
}

func TestQueueConsumeFailFastWatermarkAndDelay(t *testing.T) {
	buildQueueConsumer(t, queue.New(queue.Handle(func(m *queue.Message) error {
		if m.ID == "poison" {
			return m.Retry(90)
		}
		return nil
	}), nil))

	wm, delay, errMsg, _ := callConsume(t, queuePayload(t, "q", js.Undefined(),
		queueMsg{id: "a"}, queueMsg{id: "poison"}, queueMsg{id: "c"},
	))
	if errMsg == "" || !strings.Contains(errMsg, "queue: retry requested") {
		t.Fatalf("errMsg = %q, want retry error text", errMsg)
	}
	if wm != 1 || delay != 90 {
		t.Fatalf("watermark=%d delay=%d, want 1/90", wm, delay)
	}
}

func TestQueueConsumeNoConsumerRegistered(t *testing.T) {
	queueConsumer = nil
	t.Cleanup(func() { queueConsumer = nil })

	_, _, errMsg, _ := callConsume(t, queuePayload(t, "q", js.Undefined(), queueMsg{id: "a"}))
	if !strings.Contains(errMsg, "no queue consumer registered") {
		t.Fatalf("errMsg = %q", errMsg)
	}
}

func TestQueueConsumeRecordsEnvForCurrentEnv(t *testing.T) {
	var sawBinding bool
	buildQueueConsumer(t, queue.New(func(b *queue.Batch) error {
		env := CurrentEnv()
		if env != nil {
			if _, err := env.GetQueue("KNOWN"); err == nil {
				sawBinding = true
			}
		}
		return nil
	}, nil))

	env := js.Global().Get("Object").New()
	env.Set("KNOWN", js.Global().Get("Object").New())
	if _, _, errMsg, _ := callConsume(t, queuePayload(t, "q", env, queueMsg{id: "a"})); errMsg != "" {
		t.Fatalf("consume error: %s", errMsg)
	}
	if !sawBinding {
		t.Fatal("CurrentEnv inside the handler could not resolve KNOWN")
	}
	// After the invocation the slot must be cleared.
	if CurrentEnv() != nil {
		t.Fatal("CurrentEnv leaked past the invocation")
	}
}

func TestGetQueueMissingBindingErrors(t *testing.T) {
	env := &Env{obj: js.Global().Get("Object").New()}
	_, err := env.GetQueue("NOT_THERE")
	var qe *queue.QueueError
	if !errors.As(err, &qe) {
		t.Fatalf("err = %v, want *queue.QueueError", err)
	}
}

func TestFlushOutboxEncodesQueues(t *testing.T) {
	buildQueueConsumer(t, queue.New(func(*queue.Batch) error { return nil }, nil))

	env := &Env{obj: js.Global().Get("Object").New()}
	env.obj.Set("OUT", js.Global().Get("Object").New())
	q, err := env.GetQueue("OUT")
	if err != nil {
		t.Fatal(err)
	}
	q.Send([]byte("hello"))
	q.SendWithOptions([]byte("late"), queue.SendOptions{DelaySeconds: 30})

	res, ok := flushOutbox(js.Undefined(), nil).(js.Value)
	if !ok {
		t.Fatal("flushOutbox returned no js.Value")
	}
	if res.Get("count").Int() != 2 {
		t.Fatalf("count = %d, want 2", res.Get("count").Int())
	}
	msgs := res.Get("queues").Get("OUT")
	if msgs.Length() != 2 {
		t.Fatalf("OUT has %d messages", msgs.Length())
	}
	first := msgs.Index(0)
	buf := make([]byte, first.Get("body").Get("byteLength").Int())
	js.CopyBytesToGo(buf, first.Get("body"))
	if string(buf) != "hello" {
		t.Fatalf("first body = %q", buf)
	}
	if msgs.Index(1).Get("delaySeconds").Int() != 30 {
		t.Fatalf("delaySeconds = %v", msgs.Index(1).Get("delaySeconds"))
	}
	// Outbox drained: a second flush is empty.
	again, _ := flushOutbox(js.Undefined(), nil).(js.Value)
	if again.Get("count").Int() != 0 {
		t.Fatal("outbox not drained after flush")
	}
}

func TestEnvOfReadsBridgeStoredEnv(t *testing.T) {
	// The fetch bridge stores the raw env under its own key; EnvOf must
	// wrap whatever it finds there.
	req := &tinyworker.Request{}
	if EnvOf(req) != nil {
		t.Fatal("EnvOf on a bare request must be nil")
	}

	env := js.Global().Get("Object").New()
	env.Set("MYQ", js.Global().Get("Object").New())
	req.Set("cloudflare.env", env)

	e := EnvOf(req)
	if e == nil {
		t.Fatal("EnvOf returned nil for a request carrying an env")
	}
	if _, err := e.GetQueue("MYQ"); err != nil {
		t.Errorf("GetQueue(MYQ): %v", err)
	}
	if _, err := e.GetQueue("ABSENT"); err == nil {
		t.Error("GetQueue(ABSENT) should error: binding not in env")
	}
}
