package cloudflare

// MaxBodyBytes caps the request body size accepted by the runtime.
//
// It defaults to 10 MiB and can be changed any time before Register:
//
//	cloudflare.MaxBodyBytes = 32 << 20
//
// Requests with larger bodies are rejected with 413 before the body is
// buffered or copied across the WASM boundary. Values <= 0 disable the limit.
var MaxBodyBytes = 10 << 20
