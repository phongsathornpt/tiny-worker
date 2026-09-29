package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// route is one router.Handle registration found in source.
type route struct {
	Method  string
	Pattern string
	Pos     string // file:line
}

// cmdRoutes walks the current directory (or -dir) and prints every
// router.Handle("METHOD", "/path", ...) call it finds. This is static
// extraction, not reflection: dynamic registrations won't appear.
func cmdRoutes(args []string) error {
	flags := flag.NewFlagSet("routes", flag.ContinueOnError)
	dir := flags.String("dir", ".", "directory to scan")
	if err := flags.Parse(args); err != nil {
		return errExit2
	}

	var routes []route
	err := filepath.WalkDir(*dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == ".tools" || name == "dist" || name == ".wrangler" || name == "cmd" {
				if path != "." {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		found, err := parseRoutes(path)
		if err != nil {
			// Unparseable files (e.g. other projects) shouldn't kill the scan.
			fmt.Fprintf(os.Stderr, "tiny-worker: %s: %v\n", path, err)
			return nil
		}
		routes = append(routes, found...)
		return nil
	})
	if err != nil {
		return err
	}

	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Pattern != routes[j].Pattern {
			return routes[i].Pattern < routes[j].Pattern
		}
		return routes[i].Method < routes[j].Method
	})

	if len(routes) == 0 {
		fmt.Println("no routes found")
		return nil
	}
	w := max(len("PATTERN"), maxPatternWidth(routes))
	fmt.Printf("%-*s  %-*s  %s\n", 7, "METHOD", w, "PATTERN", "LOCATION")
	for _, r := range routes {
		fmt.Printf("%-*s  %-*s  %s\n", 7, r.Method, w, r.Pattern, r.Pos)
	}
	return nil
}

func maxPatternWidth(routes []route) int {
	w := 0
	for _, r := range routes {
		if len(r.Pattern) > w {
			w = len(r.Pattern)
		}
	}
	return w
}

// parseRoutes extracts router.Handle("METHOD", "pattern", ...) calls from one
// file. Any receiver/variable named router (or ending in router / r / route)
// calling Handle with two leading string literals counts.
func parseRoutes(path string) ([]route, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}

	var routes []route
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil || sel.Sel.Name != "Handle" {
			return true
		}
		if !looksLikeRouter(sel.X) || len(call.Args) < 2 {
			return true
		}
		method, ok1 := stringLit(call.Args[0])
		pattern, ok2 := stringLit(call.Args[1])
		if !ok1 || !ok2 {
			return true
		}
		pos := fset.Position(call.Pos())
		routes = append(routes, route{
			Method:  method,
			Pattern: pattern,
			Pos:     fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
		})
		return true
	})
	return routes, nil
}

// looksLikeRouter reports whether the expression plausibly names a router:
// `router`, `r`, `route`, or anything containing "router"/"routes".
func looksLikeRouter(x ast.Expr) bool {
	ident, ok := x.(*ast.Ident)
	if !ok {
		return false
	}
	name := ident.Name
	if name == "r" || name == "router" || name == "route" {
		return true
	}
	return strings.Contains(name, "router") || strings.Contains(name, "routes")
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	return strings.Trim(lit.Value, "`\""), true
}
