package main

// queueProjectFiles returns the additions `-queue` layers onto the REST
// scaffold: a queue consumer wired next to the HTTP app, a producer helper
// the handlers call, and the wrangler queues config (which also replaces
// the base one).
func queueProjectFiles(module, localFrameworkPath string) map[string]string {
	return map[string]string{
		"main.go":                    queueMainTmpl(module),
		"internal/queue/consumer.go": queueConsumerTmpl(),
		"internal/queue/producer.go": queueProducerTmpl(module),
		"wrangler.jsonc":             queueWranglerTmpl(),
	}
}

func queueMainTmpl(module string) string {
	return `package main

import (
	tinyworker "github.com/phongsathornpt/tiny-worker"
	"github.com/phongsathornpt/tiny-worker/router"
	"github.com/phongsathornpt/tiny-worker/runtime/cloudflare"
	"github.com/phongsathornpt/tiny-worker/runtime/cloudflare/queuebridge"
	"` + module + `/internal/handler"
	"` + module + `/internal/queue"
	"` + module + `/internal/middleware"
	"` + module + `/internal/repository"
	"` + module + `/internal/usecase"
)

func main() {
	r := router.New()
	userRepository := repository.NewMemoryUserRepository()
	userUsecase := usecase.NewUsers(userRepository)
	handler.RegisterUserRoutes(r, userUsecase)

	app := tinyworker.New(nil)
	app.UseRouter(r)
	middleware.UseDefaults(app)
	cloudflare.Register(app)
	// The queue consumer runs in the same worker; batches arrive through
	// worker.js's queue() export and share the isolate with fetch.
	queuebridge.RegisterQueue(queue.NewOrdersConsumer())
	select {}
}
`
}

func queueConsumerTmpl() string {
	return `package queue

import (
	"strings"

	"github.com/phongsathornpt/tiny-worker/queue"
)

// NewOrdersConsumer processes delivered batches with fail-fast semantics:
// the first failing message stops the batch, messages before it stay
// acked, and the tail redelivers in order (with the requested delay).
//
// Delivery is at-least-once: use m.ID to dedupe effects and m.Attempts to
// park poison messages (e.g. skip after 3 attempts) — that is the escape
// hatch from head-of-line blocking.
func NewOrdersConsumer() *queue.Consumer {
	return queue.New(func(b *queue.Batch) error {
		for i, m := range b.Messages {
			if err := process(m); err != nil {
				b.AckThrough(i - 1) // the acked prefix stays acked
				return err          // a *queue.RetryError carries a delay
			}
			b.AckThrough(i)
		}
		return nil
	}, &queue.Options{Queue: "ORDERS"})
}

// process handles one message. A *queue.RetryError (see m.Retry) asks for
// a redelivery after an optional delay; any other error fails the batch at
// this message without a delay hint.
func process(m *queue.Message) error {
	body := strings.TrimSpace(string(m.Body))
	if body == "" {
		return m.Retry(30)
	}
	// TODO: real work per message.
	return nil
}
`
}

func queueProducerTmpl(module string) string {
	return `package queue

import (
	"errors"

	tinyworker "github.com/phongsathornpt/tiny-worker"
	"github.com/phongsathornpt/tiny-worker/runtime/cloudflare/queuebridge"
)

// OrdersBinding matches wrangler.jsonc's queues.producers entry.
const OrdersBinding = "ORDERS"

// SendToOrders enqueues one message from a fetch handler. Sends join the
// invocation outbox and flush (awaited sendBatch) before the response
// returns, so a failed send fails the request — callers know the message
// was NOT enqueued.
//
//	err := queue.SendToOrders(req, body) // inside a handler: 500 on failure
func SendToOrders(req *tinyworker.Request, body []byte) error {
	env := queuebridge.EnvOf(req)
	if env == nil {
		return errors.New("queue: no env attached (not running under a Workers bridge)")
	}
	q, err := env.GetQueue(OrdersBinding)
	if err != nil {
		return err
	}
	q.Send(body)
	return nil
}
`
}

func queueWranglerTmpl() string {
	return `{
  "name": "my-worker",
  "main": "worker.js",
  "compatibility_date": "2026-09-09",
  "rules": [
    { "type": "CompiledWasm", "globs": ["**/*.wasm"], "fallthrough": true }
  ],
  "consumers": [
    {
      "queue": "my-worker-orders",
      "max_batch_size": 10,
      "max_retries": 3,
      "dead_letter_queue": "my-worker-orders-dlq"
    }
  ],
  "queues": {
    "producers": [
      { "binding": "ORDERS", "queue": "my-worker-orders" }
    ]
  }
}
`
}
