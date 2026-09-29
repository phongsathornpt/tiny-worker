package main

import "path/filepath"

// filepathFromRoot resolves p relative to the module root (two levels up
// from cmd/tiny-worker), so tests can read repo files regardless of cwd.
func filepathFromRoot(p string) string {
	return filepath.Join("..", "..", p)
}
