package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewProjectFilesContainsCleanArchitectureRESTScaffold(t *testing.T) {
	files := newProjectFiles("example.com/catalog", "/workspace/tiny-worker")
	wantFiles := []string{
		"README.md",
		"go.mod",
		"main.go",
		"internal/handler/users.go",
		"internal/model/user.go",
		"internal/repository/memory.go",
		"internal/usecase/users.go",
		"pkg/middleware/middleware.go",
		"pkg/utils/location.go",
		"wrangler.jsonc",
	}
	for _, name := range wantFiles {
		if _, ok := files[name]; !ok {
			t.Errorf("generated files missing %q", name)
		}
	}
	for name, want := range map[string]string{
		"go.mod":                        "replace github.com/phongsathornpt/tiny-worker => /workspace/tiny-worker",
		"main.go":                       "handler.RegisterUserRoutes(r, userUsecase)",
		"internal/handler/users.go":     "rest.Post(r, \"/api/v1/users\"",
		"internal/usecase/users.go":     "type UserRepository interface",
		"internal/repository/memory.go": "Worker isolates are",
		"pkg/middleware/middleware.go":  "rest.Errors()",
		"README.md":                     "Clean Architecture",
	} {
		if !strings.Contains(files[name], want) {
			t.Errorf("%s does not contain %q", name, want)
		}
	}
}

func TestWriteProjectCreatesCompleteTree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "path", "service")
	files := newProjectFiles("example.com/service", "")
	if err := writeProject(dir, files); err != nil {
		t.Fatal(err)
	}
	for name := range files {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("generated file %q: %v", name, err)
		}
	}
}

func TestWriteProjectDoesNotReplaceExistingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "service")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := writeProject(dir, map[string]string{"main.go": "package main\n"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("writeProject() error = %v, want already-exists error", err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep" {
		t.Fatalf("existing target changed: content=%q err=%v", got, err)
	}
}

func TestWriteProjectCleansUpAfterStagingFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "service")
	err := writeProject(dir, map[string]string{
		"block":      "a file, not a directory",
		"block/file": "cannot be written beneath a file",
	})
	if err == nil {
		t.Fatal("writeProject() error = nil, want staging write failure")
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("target exists after failed scaffold: %v", statErr)
	}
	entries, readErr := os.ReadDir(filepath.Dir(dir))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("staging directory leaked after failure: %v", entries)
	}
}
