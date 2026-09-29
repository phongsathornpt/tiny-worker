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
func cmdNew(args []string) error {
	// Go's flag package stops at the first positional, so pull -module out
	// manually to allow both `new dir -module x` and `new -module x dir`.
	var moduleFlag string
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
		".gitignore":                    ".wrangler/\ndist/\n*.wasm\n.tools/\n",
		"README.md":                     projectReadmeTmpl(),
		"go.mod":                        goModTmpl(module, localFrameworkPath),
		"main.go":                       projectMainTmpl(module),
		"internal/handler/users.go":     handlerTmpl(module),
		"internal/model/user.go":        modelTmpl(),
		"internal/repository/memory.go": repositoryTmpl(module),
		"internal/usecase/users.go":     usecaseTmpl(module),
		"pkg/middleware/middleware.go":  middlewareTmpl(),
		"pkg/utils/location.go":         utilsTmpl(),
		"wrangler.jsonc":                wranglerTmpl(),
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
	"` + module + `/internal/repository"
	"` + module + `/internal/usecase"
	"` + module + `/pkg/middleware"
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
	"` + module + `/internal/model"
	"` + module + `/pkg/utils"
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

// UserService is the handler's application seam.
type UserService interface {
	List() []model.User
	Get(id int) (model.User, error)
	Create(name, email string) (model.User, error)
	Update(id int, name, email string) (model.User, error)
	Delete(id int) error
}

func RegisterUserRoutes(r *router.Router, users UserService) {
	rest.Get(r, "/healthz", rest.HandlerNoInput(func(*tinyworker.Request) (map[string]string, error) {
		return map[string]string{"status": "ok"}, nil
	}))
	rest.Get(r, "/api/v1/users", rest.HandlerNoInput(func(*tinyworker.Request) ([]model.User, error) {
		return users.List(), nil
	}))
	rest.Post(r, "/api/v1/users", rest.Handler(func(_ *tinyworker.Request, in createUserRequest) (rest.Result, error) {
		user, err := users.Create(in.Name, in.Email)
		if err != nil {
			return rest.Result{}, err
		}
		return rest.Result{Status: 201, Body: user, Headers: []tinyworker.Header{{Name: "location", Value: utils.ResourceLocation("/api/v1/users", user.ID)}}}, nil
	}))
	rest.Get(r, "/api/v1/users/:id", rest.Handler(func(_ *tinyworker.Request, in userPath) (model.User, error) {
		user, err := users.Get(in.ID)
		return user, httpError(err, in.ID)
	}))
	rest.Put(r, "/api/v1/users/:id", rest.Handler(func(_ *tinyworker.Request, in updateUserRequest) (model.User, error) {
		user, err := users.Update(in.ID, in.Name, in.Email)
		return user, httpError(err, in.ID)
	}))
	rest.Delete(r, "/api/v1/users/:id", rest.Handler(func(_ *tinyworker.Request, in userPath) (rest.Result, error) {
		if err := users.Delete(in.ID); err != nil {
			return rest.Result{}, httpError(err, in.ID)
		}
		return rest.Result{Status: 204}, nil
	}))
}

func httpError(err error, id int) error {
	if errors.Is(err, model.ErrUserNotFound) {
		return tinyworker.NotFound("user " + strconv.Itoa(id) + " does not exist")
	}
	return err
}
`
}

func modelTmpl() string {
	return `package model

import "errors"

var ErrUserNotFound = errors.New("user not found")

type User struct {
	ID    int    ` + "`json:\"id\"`" + `
	Name  string ` + "`json:\"name\"`" + `
	Email string ` + "`json:\"email\"`" + `
}
`
}

func usecaseTmpl(module string) string {
	return `package usecase

import (
	"` + module + `/internal/model"
)

// UserRepository is the persistence port required by the application layer.
// Concrete adapters live in internal/repository and are wired in main.go.
type UserRepository interface {
	List() []model.User
	Get(id int) (model.User, bool)
	Create(name, email string) model.User
	Update(id int, name, email string) (model.User, bool)
	Delete(id int) bool
}

type Users struct {
	repository UserRepository
}

func NewUsers(repository UserRepository) *Users { return &Users{repository: repository} }

func (u *Users) List() []model.User { return u.repository.List() }

func (u *Users) Get(id int) (model.User, error) {
	user, ok := u.repository.Get(id)
	if !ok {
		return model.User{}, model.ErrUserNotFound
	}
	return user, nil
}

func (u *Users) Create(name, email string) (model.User, error) {
	return u.repository.Create(name, email), nil
}

func (u *Users) Update(id int, name, email string) (model.User, error) {
	user, ok := u.repository.Update(id, name, email)
	if !ok {
		return model.User{}, model.ErrUserNotFound
	}
	return user, nil
}

func (u *Users) Delete(id int) error {
	if !u.repository.Delete(id) {
		return model.ErrUserNotFound
	}
	return nil
}
`
}

func repositoryTmpl(module string) string {
	return `package repository

import (
	"sync"

	"` + module + `/internal/model"
)

// MemoryUserRepository is a scaffold example only. Worker isolates are
// ephemeral; use a durable Cloudflare service for production persistence.
type MemoryUserRepository struct {
	mu   sync.Mutex
	next int
	data map[int]model.User
}

func NewMemoryUserRepository() *MemoryUserRepository {
	return &MemoryUserRepository{data: make(map[int]model.User)}
}

func (r *MemoryUserRepository) List() []model.User {
	r.mu.Lock()
	defer r.mu.Unlock()
	users := make([]model.User, 0, len(r.data))
	for id := 1; id <= r.next; id++ {
		if user, ok := r.data[id]; ok {
			users = append(users, user)
		}
	}
	return users
}

func (r *MemoryUserRepository) Get(id int) (model.User, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	user, ok := r.data[id]
	return user, ok
}

func (r *MemoryUserRepository) Create(name, email string) model.User {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	user := model.User{ID: r.next, Name: name, Email: email}
	r.data[user.ID] = user
	return user
}

func (r *MemoryUserRepository) Update(id int, name, email string) (model.User, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return model.User{}, false
	}
	user := model.User{ID: id, Name: name, Email: email}
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

func utilsTmpl() string {
	return `package utils

import "strconv"

func ResourceLocation(collection string, id int) string {
	return collection + "/" + strconv.Itoa(id)
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

This project is scaffolded with tiny-worker and uses a Clean Architecture
layout. HTTP handlers call use cases; use cases depend on a repository
interface; ` + "`main.go`" + ` wires the concrete repository and Cloudflare runtime.

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
