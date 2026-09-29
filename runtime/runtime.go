package runtime

import tinyworker "github.com/thorn/tiny-worker"

// Adapter binds the framework core to a concrete execution environment.
type Adapter interface {
	Serve(*tinyworker.App) error
}
