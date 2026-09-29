package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// cmdNew scaffolds a minimal tiny-worker project in dir.
func cmdNew(args []string) error {
	// Go's flag package stops at the first positional, so pull -module out
	// manually to allow both `new dir -module x` and `new -module x dir`.
	var moduleFlag string
	rest := args[:0]
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
			rest = append(rest, args[i])
		}
	}
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: tiny-worker new <dir> [-module path]")
		return errExit2
	}
	dir := rest[0]
	if dir == "" || dir == "." || dir == ".." || strings.HasPrefix(dir, "/") {
		fmt.Fprintf(os.Stderr, "tiny-worker: invalid project directory %q\n", dir)
		return errExit2
	}
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s already exists", dir)
	}

	mod := moduleFlag
	if mod == "" {
		mod = "example.com/" + filepath.Base(dir)
	}

	files := map[string]string{
		"go.mod":         goModTmpl(mod),
		"main.go":        mainTmpl(),
		"wrangler.jsonc": wranglerTmpl(),
		".gitignore":     ".wrangler/\ndist/\n*.wasm\n.tools/\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
		fmt.Printf("created %s\n", path)
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

func goModTmpl(module string) string {
	return fmt.Sprintf("module %s\n\ngo 1.27\n\nrequire github.com/phongsathornpt/tiny-worker v0.0.0\n\nreplace github.com/phongsathornpt/tiny-worker => %s\n",
		module, projectRootOrWarn())
}

// projectRootOrWarn lets scaffolded projects reference the framework by path
// when the CLI runs inside the framework checkout; otherwise the module
// requires a published version (which the user can `go mod tidy` into place).
func projectRootOrWarn() string {
	if _, err := os.Stat("go.mod"); err == nil {
		if wd, err := os.Getwd(); err == nil {
			return wd
		}
	}
	return "../tiny-worker"
}

func mainTmpl() string {
	return `package main

import (
	tinyworker "github.com/phongsathornpt/tiny-worker"
	"github.com/phongsathornpt/tiny-worker/router"
	"github.com/phongsathornpt/tiny-worker/runtime/cloudflare"
)

func main() {
	r := router.New()
	r.Handle("GET", "/", func(*tinyworker.Request) (*tinyworker.Response, error) {
		return &tinyworker.Response{
			Status:  200,
			Headers: []tinyworker.Header{{Name: "content-type", Value: "text/plain"}},
			Body:    []byte("hello from tiny-worker"),
		}, nil
	})

	app := tinyworker.New(nil)
	app.UseRouter(r)
	app.Use(
		tinyworker.Recover(),
		tinyworker.CORS(tinyworker.CORSOptions{}),
	)
	cloudflare.Register(app)
	select {}
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
