package tinyworker

// requestStore holds request-scoped values, created lazily so requests that
// never use context values pay nothing.
type requestStore struct {
	values map[string]any
}

func (s *requestStore) set(key string, v any) {
	if s.values == nil {
		s.values = make(map[string]any, 2)
	}
	s.values[key] = v
}

func (s *requestStore) get(key string) (any, bool) {
	v, ok := s.values[key]
	return v, ok
}

func (s *requestStore) delete(key string) {
	delete(s.values, key)
}

type Header struct {
	Name  string
	Value string
}

type Param struct {
	Name  string
	Value string
}

type Request struct {
	Method  string
	URL     string
	Path    string
	Headers []Header
	Params  []Param
	Body    []byte

	store *requestStore
}

// Set stores a request-scoped value, visible to all later middleware and to
// the handler. Safe to call before and during request handling; the backing
// map is created on first use.
func (r *Request) Set(key string, value any) {
	if r == nil {
		return
	}
	if r.store == nil {
		r.store = &requestStore{}
	}
	r.store.set(key, value)
}

// Get returns a request-scoped value previously stored with Set, and whether
// it was present.
func (r *Request) Get(key string) (any, bool) {
	if r == nil || r.store == nil {
		return nil, false
	}
	return r.store.get(key)
}

// Delete removes a request-scoped value.
func (r *Request) Delete(key string) {
	if r == nil || r.store == nil {
		return
	}
	r.store.delete(key)
}

// Value returns the request-scoped value stored under key, type-asserted to
// T. The bool result is false when the key is absent or holds a different
// type. Typical middleware setup:
//
//	var userID = tinyworker.NewValue[int64]("auth.user_id")
//	// middleware:      userID.Set(req, 42)
//	// handler:         id, ok := userID.Get(req)
func Value[T any](key string) valueFunc[T] { return valueFunc[T]{key} }

type valueFunc[T any] struct{ key string }

// Key returns the underlying storage key.
func (f valueFunc[T]) Key() string { return f.key }

// Set stores a typed value on the request.
func (f valueFunc[T]) Set(r *Request, v T) { r.Set(f.key, v) }

// Get retrieves a typed value, ok=false when absent or a different type.
func (f valueFunc[T]) Get(r *Request) (T, bool) {
	var zero T
	raw, ok := r.Get(f.key)
	if !ok {
		return zero, false
	}
	v, ok := raw.(T)
	return v, ok
}

// Set stores a typed value under key. Convenience form of Value[T](key).Set.
func Set[T any](r *Request, key string, v T) { r.Set(key, v) }

// Get retrieves a typed value under key. Convenience form of Value[T](key).Get.
func Get[T any](r *Request, key string) (T, bool) { return Value[T](key).Get(r) }

type Response struct {
	Status  int
	Headers []Header
	Body    []byte
}
