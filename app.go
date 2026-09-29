package tinyworker

// Handler processes a request and returns a response or an error.
type Handler func(*Request) (*Response, error)

// Router is the subset of routing the App understands. *router.Router
// satisfies it: Lookup returns the handler for the request, ErrNotFound when
// no path matches, and a MethodNotAllowed StatusError when the path exists
// only under other methods.
type Router interface {
	Lookup(method, path string) (Handler, []Param, error)
}

// Middleware wraps a Handler to produce a new one, e.g. adding logging,
// panic recovery, or CORS headers. Middleware registered on the App runs
// before the router, so it also sees routing outcomes (404/405).
type Middleware func(Handler) Handler

// App is the request pipeline: middleware chain around the router (or a
// catch-all handler).
type App struct {
	handler    Handler
	router     Router
	middleware []Middleware
}

func New(handler Handler) *App {
	return &App{handler: handler}
}

// UseRouter attaches a router. Once set, Serve dispatches through it:
// unmatched paths produce 404/405 responses instead of falling through to
// the catch-all handler.
func (a *App) UseRouter(r Router) { a.router = r }

// Use appends middleware. Middleware registered first is the outermost: it
// sees the request first and its post-processing runs last, around every
// later middleware and the router/handler itself.
func (a *App) Use(m ...Middleware) { a.middleware = append(a.middleware, m...) }

// chain composes middleware around h in reverse registration order, so the
// first registered middleware is the outermost wrapper.
func chain(h Handler, mw []Middleware) Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

func (a *App) Serve(req *Request) (*Response, error) {
	if a == nil || (a.handler == nil && a.router == nil) {
		return nil, ErrNotFound
	}
	return chain(a.dispatch(), a.middleware)(req)
}

// dispatch returns the innermost request handler: the router-driven one when
// a router is attached, otherwise the catch-all handler.
func (a *App) dispatch() Handler {
	if a.router != nil {
		return a.dispatchRouter
	}
	return a.handler
}

func (a *App) dispatchRouter(req *Request) (*Response, error) {
	path := req.Path
	if path == "" {
		path = ParsePath(req.URL)
	}
	h, params, err := a.router.Lookup(req.Method, path)
	if err != nil {
		return nil, err
	}
	req.Params = params
	return h(req)
}

// ParsePath extracts the path portion of a URL, dropping the query string
// and fragment. Bare paths pass through unchanged. Implemented without
// net/url (see path.go) so the wasm stays small; net/url-based behavior is
// pinned by differential tests in path_test.go.
func ParsePath(raw string) string {
	return parseURLPath(raw)
}
