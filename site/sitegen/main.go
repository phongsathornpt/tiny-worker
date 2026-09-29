package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/a-h/templ"
	"github.com/phongsathornpt/tiny-worker/site/views"
)

func main() {
	outputs := map[string]templ.Component{
		"public/index.html":              views.Home(),
		"public/docs/index.html":         views.Docs(),
		"public/quickstart/index.html":   views.Quickstart(),
		"public/contributing/index.html": views.Contributing(),
		"rendered/fragments/hello.html":  views.HelloExampleFragment(),
		"rendered/fragments/rest.html":   views.RESTExampleFragment(),
	}
	for path, component := range outputs {
		if err := renderFile(path, component); err != nil {
			fmt.Fprintln(os.Stderr, "sitegen:", err)
			os.Exit(1)
		}
		fmt.Println("rendered", path)
	}
}

func renderFile(path string, component templ.Component) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := component.Render(context.Background(), file); err != nil {
		_ = file.Close()
		return fmt.Errorf("render %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}
