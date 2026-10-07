// Command queue is the tiny-worker Queues example: one worker that both
// produces and consumes. Producer routes accept HTTP and enqueue onto the
// ORDERS binding; the consumer processes delivered batches with fail-fast
// retry semantics and keeps in-memory stats the routes expose.
//
// From the repo root:
//
//	make wasm-queue
//	npx wrangler dev --config examples/queue/wrangler.jsonc
//
// Routes:
//
//	GET  /            index (how to produce)
//	POST /send        enqueue the raw body as one message
//	POST /send-batch  enqueue one message per line of the body
//	POST /chain       enqueue, then process the queued copy through the consumer
//	GET  /stats       what the consumer has seen (batches, acked, retried, ids)
//	POST /reset       zero the stats
//
// Consumer contract (see the README's Queues section): batches are
// processed in order, a message whose body says "poison" (or "trap") fails
// the batch at that message — "poison" requests a 60s retry delay, "trap"
// kills the runtime so the rebuild-and-rethrow path can be observed.
package main

import (
	"strconv"
	"strings"
	"sync"

	tinyworker "github.com/phongsathornpt/tiny-worker"
	"github.com/phongsathornpt/tiny-worker/queue"
	"github.com/phongsathornpt/tiny-worker/router"
	"github.com/phongsathornpt/tiny-worker/runtime/cloudflare"
	"github.com/phongsathornpt/tiny-worker/runtime/cloudflare/queuebridge"
)

// stats records what the consumer saw. Isolate-local: this is example
// plumbing, not durable storage.
type stats struct {
	mu      sync.Mutex
	batches int
	acked   int
	retried int
	lastIDs []string
}

var seen = &stats{}

func (s *stats) record(b *queue.Batch, acked int, retried bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches++
	s.acked += acked
	if retried {
		s.retried++
	}
	for _, m := range b.Messages {
		s.lastIDs = append(s.lastIDs, m.ID)
	}
	if len(s.lastIDs) > 32 {
		s.lastIDs = s.lastIDs[len(s.lastIDs)-32:]
	}
}

func (s *stats) snapshot() (batches, acked, retried int, ids []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.batches, s.acked, s.retried, append([]string(nil), s.lastIDs...)
}

// The ORDERS producer handle is resolved per request from the env; only the
// binding NAME is static, so the example compiles the same way tests run it.
const producerBinding = "ORDERS"

func text(status int, body string) (*tinyworker.Response, error) {
	return &tinyworker.Response{
		Status:  status,
		Headers: []tinyworker.Header{{Name: "content-type", Value: "text/plain; charset=utf-8"}},
		Body:    []byte(body),
	}, nil
}

func main() {
	// Consumer: fail-fast over the batch, with per-message progress so a
	// failure mid-batch keeps the acked prefix acked (and a "trap" message
	// demonstrates that only the tail redelivers).
	consumer := queue.New(func(b *queue.Batch) (err error) {
		acked := 0
		defer func() { seen.record(b, acked, err != nil) }()

		for i, m := range b.Messages {
			body := strings.ToLower(strings.TrimSpace(string(m.Body)))

			if body == "trap" && m.Attempts < 2 {
				// Kill the runtime mid-batch: worker.js rebuilds it, acks
				// the recorded prefix, and rethrows — Cloudflare redelivers
				// only the unacked tail. Attempts < 2 is the documented
				// escape hatch: the redelivery sails through instead of
				// looping until the DLQ.
				panic("queue example: deliberate trap")
			}
			if body == "poison" {
				// Fail fast here: the prefix stays acked, this message and
				// the tail redeliver after the requested delay.
				b.AckThrough(i - 1)
				return m.Retry(60)
			}

			b.AckThrough(i)
			acked = i + 1
		}
		return nil
	}, &queue.Options{Queue: producerBinding})
	consumer.Use(queue.LogBatches(func(res queue.BatchResult) {
		println("queue example: batch", itoa(res.Acked)+"/"+itoa(res.Total),
			"retried="+strconv.FormatBool(res.Retried), itoa(int(res.Duration.Milliseconds()))+"ms")
	}))

	r := router.New()

	r.Handle("GET", "/", func(*tinyworker.Request) (*tinyworker.Response, error) {
		return text(200, "tiny-worker queue example\n\nproduce:  POST /send (raw body), POST /send-batch (one per line)\nconsume:  delivered batches log under 'queue example:'\nobserve:  GET /stats, POST /reset\npoison semantics: body 'poison' retries after 60s, 'trap' kills the runtime\n")
	})

	r.Handle("POST", "/send", func(req *tinyworker.Request) (*tinyworker.Response, error) {
		env := queuebridge.EnvOf(req)
		if env == nil {
			return text(500, "no env: not running under a Workers bridge\n")
		}
		q, err := env.GetQueue(producerBinding)
		if err != nil {
			return text(500, err.Error()+"\n")
		}
		q.Send(req.Body)
		// The outbox flushes before this response returns; a failed send
		// fails the request instead of lying with a 2xx.
		return text(202, "enqueued "+strconv.Itoa(len(req.Body))+" bytes\n")
	})

	r.Handle("POST", "/send-batch", func(req *tinyworker.Request) (*tinyworker.Response, error) {
		env := queuebridge.EnvOf(req)
		q, err := env.GetQueue(producerBinding)
		if err != nil {
			return text(500, err.Error()+"\n")
		}
		lines := strings.Split(strings.TrimSpace(string(req.Body)), "\n")
		bodies := make([][]byte, 0, len(lines))
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				bodies = append(bodies, []byte(l))
			}
		}
		if len(bodies) == 0 {
			return text(400, "no messages in body\n")
		}
		q.SendBatch(bodies...)
		return text(202, "enqueued "+strconv.Itoa(len(bodies))+" messages\n")
	})

	r.Handle("GET", "/stats", func(*tinyworker.Request) (*tinyworker.Response, error) {
		batches, acked, retried, ids := seen.snapshot()
		return text(200, "batches="+strconv.Itoa(batches)+
			" acked="+strconv.Itoa(acked)+
			" retried="+strconv.Itoa(retried)+
			" last="+strings.Join(ids, ",")+"\n")
	})

	r.Handle("POST", "/reset", func(*tinyworker.Request) (*tinyworker.Response, error) {
		seen.mu.Lock()
		seen = &stats{}
		seen.mu.Unlock()
		return text(200, "stats reset\n")
	})

	app := tinyworker.New(nil)
	app.UseRouter(r)
	app.Use(
		tinyworker.LogRequests(func(method, path string, status int, ms int64) {
			println("queue example:", method, path, status, ms)
		}),
		tinyworker.Recover(),
	)

	cloudflare.Register(app)
	queuebridge.RegisterQueue(consumer)
	select {}
}

func itoa(n int) string { return strconv.Itoa(n) }
