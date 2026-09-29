//go:build js && wasm

package cloudflare

import (
	"syscall/js"

	tinyworker "github.com/phongsathornpt/tiny-worker"
)

func bytesFromJS(value js.Value) []byte {
	if value.IsUndefined() || value.IsNull() || value.Get("byteLength").Int() == 0 {
		return nil
	}
	buf := make([]byte, value.Get("byteLength").Int())
	js.CopyBytesToGo(buf, value)
	return buf
}

func headersFromJS(value js.Value) []tinyworker.Header {
	if value.IsUndefined() || value.IsNull() {
		return nil
	}
	length := value.Length()
	out := make([]tinyworker.Header, 0, length)
	for i := 0; i < length; i++ {
		pair := value.Index(i)
		out = append(out, tinyworker.Header{Name: pair.Index(0).String(), Value: pair.Index(1).String()})
	}
	return out
}
func response(status int, headers []tinyworker.Header, body []byte) js.Value {
	out := js.Global().Get("Object").New()
	out.Set("status", status)

	jsHeaders := js.Global().Get("Array").New(len(headers))
	for i := range headers {
		pair := js.Global().Get("Array").New(2)
		pair.SetIndex(0, headers[i].Name)
		pair.SetIndex(1, headers[i].Value)
		jsHeaders.SetIndex(i, pair)
	}
	out.Set("headers", jsHeaders)

	array := js.Global().Get("Uint8Array").New(len(body))
	js.CopyBytesToJS(array, body)
	out.Set("body", array)
	return out
}
