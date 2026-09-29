package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGlueMatchesRepoWorker pins the generated glue to the repo's worker.js:
// the two may only drift deliberately.
func TestGlueMatchesRepoWorker(t *testing.T) {
	repo, err := os.ReadFile(filepathFromRoot("worker.js"))
	if err != nil {
		t.Skipf("worker.js not readable from test cwd: %v", err)
	}
	want := string(repo)
	got := workerJSGlue("dist/worker.wasm")

	if got != want {
		// Show the first divergence to make drift debuggable.
		i := 0
		for i < len(got) && i < len(want) && got[i] == want[i] {
			i++
		}
		lo := i - 40
		if lo < 0 {
			lo = 0
		}
		snippet := func(s string) string {
			s = strings.ReplaceAll(s, "\n", "\\n")
			end := lo + 120
			if end > len(s) {
				end = len(s)
			}
			return s[lo:end]
		}
		t.Fatalf("generated glue diverges from worker.js at byte %d\ngot:  %s\nwant: %s", i, snippet(got), snippet(want))
	}
}

func TestLocateTinygoPrefersTools(t *testing.T) {
	if _, err := os.Stat(filepathFromRoot(".tools/tinygo/bin/tinygo")); err != nil {
		t.Skip("vendored tinygo not present")
	}
	// Run from a subdirectory to prove the upward search works.
	sub := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sub, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(filepath.Join(sub, "a", "b")); err != nil {
		t.Fatal(err)
	}
	// Not found from an unrelated subtree.
	if _, err := locateTinygo(); err == nil {
		t.Fatal("expected failure outside the project")
	}
	os.Chdir(wd)

	p, err := locateTinygo()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, ".tools") {
		t.Fatalf("expected vendored path, got %q", p)
	}
}

func TestDefaultMainPkg(t *testing.T) {
	// In the framework checkout: examples/hello exists.
	if dirExists("examples/hello") {
		if got := defaultMainPkg(); got != "./examples/hello" {
			t.Fatalf("defaultMainPkg() = %q, want ./examples/hello", got)
		}
	}
}
