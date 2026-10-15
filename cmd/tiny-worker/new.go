package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const frameworkModule = "github.com/phongsathornpt/tiny-worker"

// cmdNew scaffolds a REST API project using Clean Architecture in dir.
// With -queue, the scaffold also consumes and produces on a Cloudflare
// Queue: a consumer in internal/queue, producer routes, and the wrangler
// queues config.
func cmdNew(args []string) error {
	// Go's flag package stops at the first positional, so pull -module and
	// -queue out manually to allow both `new dir -module x` and
	// `new -module x dir`.
	var moduleFlag string
	wantQueue := false
	restArgs := args[:0]
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-module":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "tiny-worker: -module requires a value")
				return errExit2
			}
			moduleFlag = args[i+1]
			i++
		case strings.HasPrefix(args[i], "-module="):
			moduleFlag = strings.TrimPrefix(args[i], "-module=")
		case args[i] == "-queue":
			wantQueue = true
		default:
			restArgs = append(restArgs, args[i])
		}
	}
	if len(restArgs) != 1 {
		fmt.Fprintln(os.Stderr, "usage: tiny-worker new <dir> [-module path]")
		return errExit2
	}
	dir := restArgs[0]
	if dir == "" || dir == "." || dir == ".." || filepath.IsAbs(dir) {
		fmt.Fprintf(os.Stderr, "tiny-worker: invalid project directory %q\n", dir)
		return errExit2
	}
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s already exists", dir)
	} else if !os.IsNotExist(err) {
		return err
	}

	mod := moduleFlag
	if mod == "" {
		mod = "example.com/" + filepath.Base(filepath.Clean(dir))
	}
	files := newProjectFiles(mod, localFrameworkRoot())
	if wantQueue {
		for name, content := range queueProjectFiles(mod, localFrameworkRoot()) {
			files[name] = content
		}
	}
	if err := writeProject(dir, files); err != nil {
		return err
	}

	fmt.Printf(`
Project ready:
  cd %s
  tiny-worker build
  tiny-worker dev      # local dev server
  tiny-worker deploy   # ship it
`, dir)
	return nil
}

// writeProject stages the complete project beside its destination, then
// renames it into place so a write failure cannot leave a partial scaffold.
func writeProject(dir string, files map[string]string) error {
	parent := filepath.Dir(filepath.Clean(dir))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create project parent directory: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".tiny-worker-new-*")
	if err != nil {
		return fmt.Errorf("create project staging directory: %w", err)
	}
	defer os.RemoveAll(stage)

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(stage, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Join(dir, name), err)
		}
		if err := os.WriteFile(path, []byte(files[name]), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", filepath.Join(dir, name), err)
		}
	}
	if err := os.Rename(stage, dir); err != nil {
		if _, statErr := os.Stat(dir); statErr == nil {
			return fmt.Errorf("%s already exists", dir)
		}
		return fmt.Errorf("create project %s: %w", dir, err)
	}

	for _, name := range names {
		fmt.Printf("created %s\n", filepath.Join(dir, name))
	}
	return nil
}

func newProjectFiles(module, localFrameworkPath string) map[string]string {
	return map[string]string{
		".gitignore":                        ".wrangler/\ndist/\n*.wasm\n.tools/\n",
		"README.md":                         projectReadmeTmpl(),
		"go.mod":                            goModTmpl(module, localFrameworkPath),
		"main.go":                           projectMainTmpl(module),
		"internal/domain/user.go":           domainTmpl(),
		"internal/handler/users.go":         handlerTmpl(module),
		"internal/middleware/middleware.go": middlewareTmpl(),
		"internal/repository/memory.go":     repositoryTmpl(module),
		"internal/usecase/users.go":         usecaseTmpl(module),
		"wrangler.jsonc":                    wranglerTmpl(),
	}
}

func goModTmpl(module, localFrameworkPath string) string {
	text := "module " + module + "\n\ngo 1.27\n\nrequire " + frameworkModule + " v0.1.0\n"
	if localFrameworkPath != "" {
		text += "\nreplace " + frameworkModule + " => " + filepath.ToSlash(localFrameworkPath) + "\n"
	}
	return text
}

// localFrameworkRoot returns a local replace only when the current directory
// is the tiny-worker source module. Installed CLI users get the published
// framework version declared in go.mod.
func localFrameworkRoot() string {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" && fields[1] == frameworkModule {
			if wd, err := os.Getwd(); err == nil {
				return wd
			}
		}
	}
	return ""
}

func projectMainTmpl(module string) string {
	return `package main

import (
	tinyworker "github.com/phongsathornpt/tiny-worker"
	"github.com/phongsathornpt/tiny-worker/router"
	"github.com/phongsathornpt/tiny-worker/runtime/cloudflare"
	"` + module + `/internal/handler"
	"` + module + `/internal/middleware"
	"` + module + `/internal/repository"
	"` + module + `/internal/usecase"
)

func main() {
	r := router.New()
	userRepository := repository.NewMemoryUserRepository()
	userUsecase := usecase.NewUsers(userRepository)
	handler.RegisterUserRoutes(r, userUsecase)

	app := tinyworker.New(nil)
	app.UseRouter(r)
	middleware.UseDefaults(app)
	cloudflare.Register(app)
	select {}
}
`
}

func handlerTmpl(module string) string {
	return `package handler

import (
	"errors"
	"strconv"

	tinyworker "github.com/phongsathornpt/tiny-worker"
	"github.com/phongsathornpt/tiny-worker/rest"
	"github.com/phongsathornpt/tiny-worker/router"
	"` + module + `/internal/domain"
)

type userPath struct {
	ID int ` + "`param:\"id\"`" + `
}

type createUserRequest struct {
	Name  string ` + "`json:\"name\" validate:\"required,min=2,max=64\"`" + `
	Email string ` + "`json:\"email\" validate:\"required,email\"`" + `
}

type updateUserRequest struct {
	ID    int    ` + "`param:\"id\"`" + `
	Name  string ` + "`json:\"name\" validate:\"required,min=2,max=64\"`" + `
	Email string ` + "`json:\"email\" validate:\"required,email\"`" + `
}

// userResponse is an HTTP representation of the domain user. JSON tags and
// other transport details stay outside internal/domain.
type userResponse struct {
	ID    int    ` + "`json:\"id\"`" + `
	Name  string ` + "`json:\"name\"`" + `
	Email string ` + "`json:\"email\"`" + `
}

func presentUser(user domain.User) userResponse {
	return userResponse{ID: user.ID, Name: user.Name, Email: user.Email}
}

func presentUsers(users []domain.User) []userResponse {
	responses := make([]userResponse, 0, len(users))
	for _, user := range users {
		responses = append(responses, presentUser(user))
	}
	return responses
}

// UserService is the handler's application seam.
type UserService interface {
	List() []domain.User
	Get(id int) (domain.User, error)
	Create(name, email string) (domain.User, error)
	Update(id int, name, email string) (domain.User, error)
	Delete(id int) error
}

func RegisterUserRoutes(r *router.Router, users UserService) {
	rest.Get(r, "/healthz", rest.HandlerNoInput(func(*tinyworker.Request) (map[string]string, error) {
		return map[string]string{"status": "ok"}, nil
	}))
	rest.Get(r, "/api/v1/users", rest.HandlerNoInput(func(*tinyworker.Request) ([]userResponse, error) {
		return presentUsers(users.List()), nil
	}))
	rest.Post(r, "/api/v1/users", rest.Handler(func(_ *tinyworker.Request, in createUserRequest) (rest.Result, error) {
		user, err := users.Create(in.Name, in.Email)
		if err != nil {
			return rest.Result{}, err
		}
		return rest.Result{Status: 201, Body: presentUser(user), Headers: []tinyworker.Header{{Name: "location", Value: resourceLocation("/api/v1/users", user.ID)}}}, nil
	}))
	rest.Get(r, "/api/v1/users/:id", rest.Handler(func(_ *tinyworker.Request, in userPath) (userResponse, error) {
		user, err := users.Get(in.ID)
		return presentUser(user), httpError(err, in.ID)
	}))
	rest.Put(r, "/api/v1/users/:id", rest.Handler(func(_ *tinyworker.Request, in updateUserRequest) (userResponse, error) {
		user, err := users.Update(in.ID, in.Name, in.Email)
		return presentUser(user), httpError(err, in.ID)
	}))
	rest.Delete(r, "/api/v1/users/:id", rest.Handler(func(_ *tinyworker.Request, in userPath) (rest.Result, error) {
		if err := users.Delete(in.ID); err != nil {
			return rest.Result{}, httpError(err, in.ID)
		}
		return rest.Result{Status: 204}, nil
	}))
}

func httpError(err error, id int) error {
	if errors.Is(err, domain.ErrUserNotFound) {
		return tinyworker.NotFound("user " + strconv.Itoa(id) + " does not exist")
	}
	return err
}

func resourceLocation(collection string, id int) string {
	return collection + "/" + strconv.Itoa(id)
}
`
}

func domainTmpl() string {
	return `// Package domain is the single source of truth for business entities,
// domain errors, and repository contracts. Outer layers depend on these types;
// transport DTOs belong in their adapters.
package domain

import "errors"

var ErrUserNotFound = errors.New("user not found")

type User struct {
	ID    int
	Name  string
	Email string
}

// UserRepository is the persistence contract required by the application.
// Implementations live in outer adapter packages.
type UserRepository interface {
	List() []User
	Get(id int) (User, bool)
	Create(name, email string) User
	Update(id int, name, email string) (User, bool)
	Delete(id int) bool
}
`
}

func usecaseTmpl(module string) string {
	return `package usecase

import (
	"` + module + `/internal/domain"
)

type Users struct {
	repository domain.UserRepository
}

func NewUsers(repository domain.UserRepository) *Users { return &Users{repository: repository} }

func (u *Users) List() []domain.User { return u.repository.List() }

func (u *Users) Get(id int) (domain.User, error) {
	user, ok := u.repository.Get(id)
	if !ok {
		return domain.User{}, domain.ErrUserNotFound
	}
	return user, nil
}

func (u *Users) Create(name, email string) (domain.User, error) {
	return u.repository.Create(name, email), nil
}

func (u *Users) Update(id int, name, email string) (domain.User, error) {
	user, ok := u.repository.Update(id, name, email)
	if !ok {
		return domain.User{}, domain.ErrUserNotFound
	}
	return user, nil
}

func (u *Users) Delete(id int) error {
	if !u.repository.Delete(id) {
		return domain.ErrUserNotFound
	}
	return nil
}
`
}

func repositoryTmpl(module string) string {
	return `package repository

import (
	"sync"

	"` + module + `/internal/domain"
)

// MemoryUserRepository is a scaffold example only. Worker isolates are
// ephemeral; use a durable Cloudflare service for production persistence.
type MemoryUserRepository struct {
	mu   sync.Mutex
	next int
	data map[int]domain.User
}

func NewMemoryUserRepository() *MemoryUserRepository {
	return &MemoryUserRepository{data: make(map[int]domain.User)}
}

func (r *MemoryUserRepository) List() []domain.User {
	r.mu.Lock()
	defer r.mu.Unlock()
	users := make([]domain.User, 0, len(r.data))
	for id := 1; id <= r.next; id++ {
		if user, ok := r.data[id]; ok {
			users = append(users, user)
		}
	}
	return users
}

func (r *MemoryUserRepository) Get(id int) (domain.User, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	user, ok := r.data[id]
	return user, ok
}

func (r *MemoryUserRepository) Create(name, email string) domain.User {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	user := domain.User{ID: r.next, Name: name, Email: email}
	r.data[user.ID] = user
	return user
}

func (r *MemoryUserRepository) Update(id int, name, email string) (domain.User, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return domain.User{}, false
	}
	user := domain.User{ID: id, Name: name, Email: email}
	r.data[id] = user
	return user, true
}

func (r *MemoryUserRepository) Delete(id int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return false
	}
	delete(r.data, id)
	return true
}
`
}

func middlewareTmpl() string {
	return `package middleware

import (
	tinyworker "github.com/phongsathornpt/tiny-worker"
	"github.com/phongsathornpt/tiny-worker/rest"
)

// UseDefaults installs a REST error envelope, request logging, panic recovery,
// and permissive CORS. Registering Errors first keeps one error contract.
func UseDefaults(app *tinyworker.App) {
	app.Use(
		rest.Errors(),
		tinyworker.LogRequests(func(method, path string, status int, ms int64) {
			println("tiny-worker:", method, path, status, ms)
		}),
		tinyworker.Recover(),
		tinyworker.CORS(tinyworker.CORSOptions{}),
	)
}
`
}

func wranglerTmpl() string {
	return `{
  "name": "my-worker",
  "main": "worker.js",
  "compatibility_date": "2026-09-09",
  "rules": [
    { "type": "CompiledWasm", "globs": ["**/*.wasm"], "fallthrough": true }
  ]
}
`
}

func projectReadmeTmpl() string {
	return `# Cloudflare REST API worker

This project is scaffolded with tiny-worker and uses a strict Clean Architecture
layout. ` + "`internal/domain`" + ` is the single source of truth for business
entities, domain errors, and repository contracts. HTTP request and response
types belong to the handler and never replace the domain entity.

- ` + "`internal/domain`" + ` owns domain types and repository contracts.
- ` + "`internal/usecase`" + ` implements application behavior using domain contracts.
- ` + "`internal/handler`" + ` translates HTTP requests and responses to use cases.
- ` + "`internal/repository`" + ` contains persistence adapters that implement domain contracts.
- ` + "`internal/middleware`" + ` contains worker-wide HTTP middleware.
- ` + "`main.go`" + ` wires adapters and the Cloudflare runtime; dependencies point inward.

The included in-memory repository is for local examples. Cloudflare Worker
isolates can be restarted, so use a durable service such as D1, KV, or a
Durable Object for production data.

## Routes

- ` + "`GET /api/v1/users`" + ` — list users
- ` + "`POST /api/v1/users`" + ` — create a user
- ` + "`GET /api/v1/users/:id`" + ` — get a user
- ` + "`PUT /api/v1/users/:id`" + ` — replace a user
- ` + "`DELETE /api/v1/users/:id`" + ` — delete a user
- ` + "`GET /healthz`" + ` — health check

## Run

Install TinyGo and Wrangler, then run:

` + "```sh\ntiny-worker build\ntiny-worker dev\n```" + `

Deploy with ` + "`tiny-worker deploy`" + ` after configuring the Wrangler project name.
`
}
