package cloudflare

import (
	"strings"
	"syscall/js"
	"testing"
)

// invokeStatus renders a prepared error through the bridge's errorResponse
// mapping, the same path a router miss or handler error takes.
func invokeStatus(t *testing.T, err error) (status int, headers map[string]string, body []byte) {
	t.Helper()
	out := errorResponse(err).(js.Value)
	status = out.Get("status").Int()

	headers = map[string]string{}
	jsHeaders := out.Get("headers")
	for i := 0; i < jsHeaders.Length(); i++ {
		pair := jsHeaders.Index(i)
		name := pair.Index(0).String()
		headers[name] = pair.Index(1).String()
		headers[strings.ToLower(name)] = pair.Index(1).String()
	}

	length := out.Get("body").Get("byteLength").Int()
	body = make([]byte, length)
	js.CopyBytesToGo(body, out.Get("body"))
	return status, headers, body
}
