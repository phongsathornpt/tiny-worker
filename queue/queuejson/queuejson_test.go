package queuejson

import (
	"errors"
	"strings"
	"testing"

	"github.com/phongsathornpt/tiny-worker/queue"
)

func TestDecodeJSON(t *testing.T) {
	m := &queue.Message{ID: "m1", Body: []byte(`{"name":"Ada"}`)}
	var v struct {
		Name string `json:"name"`
	}
	if err := Decode(m, &v); err != nil {
		t.Fatal(err)
	}
	if v.Name != "Ada" {
		t.Fatalf("Name = %q", v.Name)
	}
}

func TestDecodeRejectsTrailingData(t *testing.T) {
	m := &queue.Message{ID: "m1", Body: []byte(`{"a":1} garbage`)}
	var v map[string]any
	if err := Decode(m, &v); err == nil || !strings.Contains(err.Error(), "trailing data") {
		t.Fatalf("err = %v, want trailing-data error", err)
	}
}

func TestDecodeReportsBadJSONWithMessageID(t *testing.T) {
	m := &queue.Message{ID: "poison-1", Body: []byte(`{not json`)}
	var v map[string]any
	err := Decode(m, &v)
	if err == nil || !strings.Contains(err.Error(), "poison-1") {
		t.Fatalf("err = %v, want message id in error", err)
	}
}

func TestHandleBatchDecodeAcksPrefixAndStops(t *testing.T) {
	type payload struct {
		N int `json:"n"`
	}
	h := HandleBatchDecode(func(b *queue.Batch, v payload) error {
		if v.N == 2 {
			return errors.New("boom on 2")
		}
		return nil
	})
	b := &queue.Batch{Messages: []*queue.Message{
		{ID: "a", Body: []byte(`{"n":1}`)},
		{ID: "b", Body: []byte(`{"n":2}`)},
		{ID: "c", Body: []byte(`{"n":3}`)},
	}}
	err := h(b)
	if err == nil {
		t.Fatal("expected failure at n=2")
	}
	// A plain handler error stops the batch (fail-fast) but is only wrapped
	// as retry when it comes through queue.Handle; HandleBatchDecode passes
	// user errors through unchanged so handlers control their own semantics.
	if !strings.Contains(err.Error(), "boom on 2") {
		t.Fatalf("err = %v", err)
	}
	if got := b.AckedCount(); got != 1 {
		t.Fatalf("acked = %d, want 1", got)
	}
}

func TestHandleBatchDecodeRetryOnPoison(t *testing.T) {
	type payload struct {
		N int `json:"n"`
	}
	processed := 0
	h := HandleBatchDecode(func(b *queue.Batch, v payload) error {
		processed++
		return nil
	})
	b := &queue.Batch{Messages: []*queue.Message{
		{ID: "ok", Body: []byte(`{"n":1}`)},
		{ID: "poison", Body: []byte(`not-json`)},
	}}
	err := h(b)
	var re *queue.RetryError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want retry for poison message", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1 (stopped at poison)", processed)
	}
}

func TestHandleBatchDecodePassesBatch(t *testing.T) {
	type payload struct{}
	var sawQueue string
	h := HandleBatchDecode(func(b *queue.Batch, v payload) error {
		sawQueue = b.Queue
		return nil
	})
	b := &queue.Batch{Queue: "named", Messages: []*queue.Message{{ID: "a", Body: []byte(`{}`)}}}
	if err := h(b); err != nil {
		t.Fatal(err)
	}
	if sawQueue != "named" {
		t.Fatalf("handler saw queue %q", sawQueue)
	}
}
