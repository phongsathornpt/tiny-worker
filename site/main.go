package main

import (
	"embed"

	tinyworker "github.com/phongsathornpt/tiny-worker"
	"github.com/phongsathornpt/tiny-worker/router"
	"github.com/phongsathornpt/tiny-worker/runtime/cloudflare"
	"github.com/phongsathornpt/tiny-worker/site/handler"
)

//go:embed rendered/fragments/*.html
var renderedFragments embed.FS

func main() {
	r := router.New()
	if err := handler.Register(r, renderedFragments); err != nil {
		panic(err)
	}

	app := tinyworker.New(nil)
	app.UseRouter(r)
	app.Use(
		tinyworker.LogRequests(func(method, path string, status int, ms int64) {
			println("tiny-worker-site:", method, path, status, ms)
		}),
		tinyworker.Recover(),
	)
	cloudflare.Register(app)
	select {}
}
