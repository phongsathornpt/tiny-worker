//go:build !js || !wasm

package cloudflare

import tinyworker "github.com/phongsathornpt/tiny-worker"

// Register is a no-op outside the WASM target. The real bridge in bridge.go
// applies to any js/wasm target (stock Go and TinyGo); the WASM-free build
// needs a placeholder so packages that import the runtime still compile.
func Register(_ *tinyworker.App) {}
