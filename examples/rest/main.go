// Command rest is the tiny-worker REST example: JSON in, JSON out, with binding
// and validation, an in-memory store, and one error envelope.
//
// From the repo root:
//
//	tiny-worker build -main ./examples/rest && tiny-worker dev
//
// Routes:
//
//	GET    /                    index (primitives: rest.OK)
//	GET    /users               list; ?limit=&tag= (query binding, slices)
//	POST   /users               create; validated body (400 vs 422)
//	GET    /users/:id           fetch; 404 envelope when missing
//	PUT    /users/:id           replace; path param + validated body in one struct
//	DELETE /users/:id           204
//	GET    /boom                deliberate panic (trap + rebuild on Workers)
package main

import (
	"reflect"
	"strconv"
	"strings"
	"sync"

	tinyworker "github.com/phongsathornpt/tiny-worker"
	"github.com/phongsathornpt/tiny-worker/rest"
	"github.com/phongsathornpt/tiny-worker/router"
	"github.com/phongsathornpt/tiny-worker/runtime/cloudflare"
)

// init registers a domain rule: handles are lowercase letters, digits, and
// dashes only.
func init() {
	rest.RegisterRule("handle", func(field string, v reflect.Value, _ string) *rest.FieldError {
		if v.Kind() != reflect.String {
			return nil
		}
		s := v.String()
		for i := 0; i < len(s); i++ {
			c := s[i]
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
				continue
			}
			return &rest.FieldError{
				Field:   field,
				Code:    "handle",
				Message: "must contain only lowercase letters, digits, or dashes",
			}
		}
		return nil
	})
}

type User struct {
	ID    int      `json:"id"`
	Name  string   `json:"name"`
	Email string   `json:"email"`
	Tags  []string `json:"tags,omitempty"`
}

// CreateUser is the POST /users payload. Unknown JSON fields are rejected by
// default, so a typo like "nmae" fails with 400 invalid_json instead of
// silently creating an empty user.
type CreateUser struct {
	Name   string   `json:"name" validate:"required,min=2,max=64"`
	Email  string   `json:"email" validate:"required,email"`
	Handle string   `json:"handle" validate:"required,handle"`
	Tags   []string `json:"tags,omitempty" validate:"max=5"`
}

// Validate adds a rule that is awkward to express as a tag.
func (c CreateUser) Validate() error {
	if strings.EqualFold(c.Name, "admin") {
		return rest.ValidationErrors{{Field: "name", Code: "reserved", Message: "name is reserved"}}
	}
	return nil
}

// UpdateUser shows one struct carrying both sources: the path parameter and
// the JSON body. Path params are bound after the body, so they win.
type UpdateUser struct {
	ID    int      `param:"id"`
	Name  string   `json:"name" validate:"required,min=2,max=64"`
	Email string   `json:"email" validate:"required,email"`
	Tags  []string `json:"tags,omitempty" validate:"max=5"`
}

type userPath struct {
	ID int `param:"id"`
}

type listQuery struct {
	Limit int      `query:"limit" validate:"max=100"`
	Tag   []string `query:"tag"`
}

type index struct {
	Routes []string `json:"routes"`
	Docs   string   `json:"docs"`
}

var store = newUserStore()

type userStore struct {
	mu   sync.Mutex
	next int
	byID map[int]User
}

func newUserStore() *userStore { return &userStore{byID: map[int]User{}} }

func (s *userStore) create(in CreateUser) User {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	u := User{ID: s.next, Name: in.Name, Email: in.Email, Tags: in.Tags}
	s.byID[u.ID] = u
	return u
}

func (s *userStore) get(id int) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.byID[id]
	return u, ok
}

func (s *userStore) put(id int, in UpdateUser) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[id]; !ok {
		return User{}, false
	}
	u := User{ID: id, Name: in.Name, Email: in.Email, Tags: in.Tags}
	s.byID[id] = u
	return u, true
}

func (s *userStore) delete(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[id]; !ok {
		return false
	}
	delete(s.byID, id)
	return true
}

func (s *userStore) list(limit int, tags []string) []User {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]User, 0, len(s.byID))
	for id := 1; id <= s.next; id++ {
		u, ok := s.byID[id]
		if !ok {
			continue
		}
		if len(tags) > 0 && !hasAnyTag(u, tags) {
			continue
		}
		out = append(out, u)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out
}

func hasAnyTag(u User, tags []string) bool {
	for _, want := range tags {
		for _, have := range u.Tags {
			if have == want {
				return true
			}
		}
	}
	return false
}

func main() {
	r := router.New()

	// Primitives path: hand-rolled responses still get the codec and status
	// helpers when that is all a route needs.
	rest.Get(r, "/", func(req *tinyworker.Request) (*tinyworker.Response, error) {
		return rest.OK(index{
			Routes: []string{"GET /users", "POST /users", "GET /users/:id", "PUT /users/:id", "DELETE /users/:id"},
			Docs:   "unknown fields are rejected; bad input is 400 invalid_json, failed rules are 422 validation_failed",
		})
	})

	rest.Get(r, "/users", rest.Handler(listUsers))
	rest.Post(r, "/users", rest.Handler(createUser))
	rest.Get(r, "/users/:id", rest.Handler(getUser))
	rest.Put(r, "/users/:id", rest.Handler(putUser))
	rest.Delete(r, "/users/:id", rest.Handler(deleteUser))

	rest.Get(r, "/boom", rest.HandlerNoInput(func(*tinyworker.Request) (User, error) {
		panic("boom: deliberate panic")
	}))

	app := tinyworker.New(nil)
	app.UseRouter(r)
	// Errors is registered first so it is the outermost middleware: routing
	// misses (404/405), handler errors, and recovered panics all render in the
	// envelope, one contract for every failure.
	app.Use(
		rest.Errors(),
		tinyworker.LogRequests(func(method, path string, status int, ms int64) {
			println("tiny-worker:", method, path, status, ms)
		}),
		tinyworker.Recover(),
	)

	cloudflare.Register(app)
	select {}
}

func listUsers(req *tinyworker.Request, q listQuery) ([]User, error) {
	return store.list(q.Limit, q.Tag), nil
}

func createUser(req *tinyworker.Request, in CreateUser) (rest.Result, error) {
	u := store.create(in)
	return rest.Result{
		Status:  201,
		Body:    u,
		Headers: []tinyworker.Header{{Name: "location", Value: "/users/" + strconv.Itoa(u.ID)}},
	}, nil
}

func getUser(req *tinyworker.Request, p userPath) (User, error) {
	u, ok := store.get(p.ID)
	if !ok {
		return User{}, tinyworker.NotFound("user " + strconv.Itoa(p.ID) + " does not exist")
	}
	return u, nil
}

func putUser(req *tinyworker.Request, in UpdateUser) (User, error) {
	u, ok := store.put(in.ID, in)
	if !ok {
		return User{}, tinyworker.NotFound("user " + strconv.Itoa(in.ID) + " does not exist")
	}
	return u, nil
}

func deleteUser(req *tinyworker.Request, p userPath) (rest.Result, error) {
	if !store.delete(p.ID) {
		return rest.Result{}, tinyworker.NotFound("user " + strconv.Itoa(p.ID) + " does not exist")
	}
	return rest.Result{Status: 204}, nil
}
