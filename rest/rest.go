// Package rest adds a small REST layer on top of tiny-worker: JSON responses
// behind a pluggable codec, request binding (JSON body, path params, query),
// input validation, and one error envelope.
//
// Everything here is additive — the core framework is untouched. Importing
// this package does link a JSON codec into the wasm, so workers that never
// touch REST keep their lean size.
//
// The wire contract is documented in the repo README; in short:
//
//   - 400 invalid_json / bad_request — the payload could not be parsed or bound
//   - 422 validation_failed — parsed fine, failed validation rules
//   - 404 not_found, 405 method_not_allowed, 500 internal_error
//
// All of them render as {"error":{"code","message","details"}}, where details
// carries ordered per-field problems with JSON dot paths (address.city).
//
// Typical setup:
//
//	r := router.New()
//	app := tinyworker.New(nil)
//	app.UseRouter(r)
//	app.Use(rest.Errors(), tinyworker.LogRequests(logf))
//
//	rest.Post(r, "/users", rest.Handler(createUser))          // typed body handler
//	rest.Get(r, "/users", rest.HandlerNoInput(listUsers))     // no input payload
//
// with handlers shaped like:
//
//	func createUser(req *tinyworker.Request, in CreateUser) (User, error)
//	func listUsers(req *tinyworker.Request) ([]User, error)
package rest

// config carries the per-call settings resolved from Options.
type config struct {
	codec  Codec
	strict bool
}

func newConfig(opts []Option) config {
	cfg := config{codec: DefaultCodec, strict: true}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.codec == nil {
		cfg.codec = DefaultCodec
	}
	return cfg
}

// Option configures a single REST operation (binding, rendering, validation,
// or a registered route).
type Option func(*config)

// WithCodec overrides the JSON codec for this operation. The package-level
// DefaultCodec applies when no option is given.
func WithCodec(c Codec) Option { return func(cfg *config) { cfg.codec = c } }

// Strict controls whether decoding rejects unknown fields (default true).
// Strictness is applied by codecs implementing StrictCodec; other codecs
// decode leniently and the option has no effect.
func Strict(enabled bool) Option { return func(cfg *config) { cfg.strict = enabled } }
