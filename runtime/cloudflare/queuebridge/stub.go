//go:build !js || !wasm

package queuebridge

import (
	tinyworker "github.com/phongsathornpt/tiny-worker"
	"github.com/phongsathornpt/tiny-worker/queue"
)

// Native counterparts of the js/wasm bridge so queue workers compile (and
// unit-test) outside the browser: RegisterQueue is a no-op, env resolution
// reports "no bridge", and CurrentEnv stays nil.

func RegisterQueue(_ *queue.Consumer) {}

// Env is the native counterpart of the bridge env: present for API parity,
// but no bindings resolve.
type Env struct{}

// EnvOf returns nil outside a Workers bridge.
func EnvOf(_ *tinyworker.Request) *Env { return nil }

// CurrentEnv returns nil outside a Workers bridge.
func CurrentEnv() *Env { return nil }

// GetQueue always errors in native builds: there is no queue service.
func (e *Env) GetQueue(name string) (*queue.Queue, error) {
	return nil, &queue.QueueError{Queue: name, Err: "no Workers bridge in native builds"}
}
