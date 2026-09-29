package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// buildPlan captures everything the build command needs.
type buildPlan struct {
	mainPkg   string // Go package to compile (default ./examples/hello or .)
	outDir    string // default dist
	entryName string // wrangler main (default worker.js)
	panicMode string // tinygo -panic strategy: trap (default) or print
	gc        string // tinygo -gc strategy: conservative (default) or leaking
	optLevel  string // wasm-opt level: Oz (default), O2, O3, or none to skip
}

func (p buildPlan) tinygoFlags() []string {
	flags := []string{"build", "-target", "wasm", "-opt", "z", "-no-debug"}
	if p.panicMode != "" {
		flags = append(flags, "-panic", p.panicMode)
	}
	if p.gc != "" {
		flags = append(flags, "-gc", p.gc)
	}
	return flags
}

// addBuildFlags registers the optimization knobs directly onto the plan.
// -main/-out are bound separately and copied in after Parse (see newPlan).
func addBuildFlags(fs *flag.FlagSet, plan *buildPlan) {
	fs.StringVar(&plan.panicMode, "panic", "trap", "TinyGo panic strategy: trap (smaller; panics trap the runtime) or print (tinygo default)")
	fs.StringVar(&plan.gc, "gc", "conservative", "TinyGo garbage collector: conservative or leaking (smallest/fastest, never frees)")
	fs.StringVar(&plan.optLevel, "opt", "Oz", "wasm-opt optimization: Oz, O2, O3, or none to skip")
}

// parseFlags parses args, treating -h/--help as a successful (already printed)
// exit rather than a usage error.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errHelpShown
		}
		return errExit2
	}
	return nil
}

// finish copies the -main/-out values that were parsed into the plan. They
// are bound to locals because the plan's knob fields are registered directly.
func (p *buildPlan) finish(mainPkg, out *string) {
	p.mainPkg, p.outDir = *mainPkg, *out
}

func cmdBuild(args []string) error {
	fs := flagSet("build")
	mainPkg := fs.String("main", defaultMainPkg(), "Go package to build")
	out := fs.String("out", "dist", "output directory")
	plan := &buildPlan{}
	addBuildFlags(fs, plan)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	plan.finish(mainPkg, out)
	_, err := buildWasm(*plan)
	return err
}

func cmdDeploy(args []string) error {
	fs := flagSet("deploy")
	mainPkg := fs.String("main", defaultMainPkg(), "Go package to build")
	out := fs.String("out", "dist", "output directory")
	plan := &buildPlan{}
	addBuildFlags(fs, plan)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	plan.finish(mainPkg, out)
	if _, err := buildWasm(*plan); err != nil {
		return err
	}
	return runWrangler("deploy")
}

func cmdDev(args []string) error {
	fs := flagSet("dev")
	mainPkg := fs.String("main", defaultMainPkg(), "Go package to build")
	out := fs.String("out", "dist", "output directory")
	port := fs.String("port", "8787", "local port")
	plan := &buildPlan{}
	addBuildFlags(fs, plan)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	plan.finish(mainPkg, out)
	if _, err := buildWasm(*plan); err != nil {
		return err
	}
	return runWrangler("dev", "--port", *port)
}

func flagSet(name string) *flag.FlagSet { return flag.NewFlagSet(name, flag.ContinueOnError) }

// locateWasmOpt finds binaryen's wasm-opt: a vendored
// .tools/binaryen/bin/wasm-opt (searched from cwd upward, like locateTinygo)
// first, then $PATH. Returns "" when absent — binaryen is optional tooling.
func locateWasmOpt() string {
	if wd, err := os.Getwd(); err == nil {
		for dir := wd; ; dir = filepath.Dir(dir) {
			candidate := filepath.Join(dir, ".tools", "binaryen", "bin", "wasm-opt")
			if p, err := exec.LookPath(candidate); err == nil {
				return p
			}
			if dir == filepath.Dir(dir) {
				break // reached root
			}
		}
	}
	if p, err := exec.LookPath("wasm-opt"); err == nil {
		return p
	}
	return ""
}

// runWasmOpt applies Binaryen optimization in place. binaryen is optional
// tooling: a missing wasm-opt downgrades to a loud warning (CI's size gate
// keeps optimized/unoptimized artifacts honest), but a failing run is an
// error — a silently unoptimized artifact must never masquerade as a good
// build when the tool is present.
func runWasmOpt(plan buildPlan, wasmPath string) error {
	level := plan.optLevel
	if level == "" {
		level = "Oz"
	}
	if strings.EqualFold(level, "none") {
		return nil
	}
	wasmOpt := locateWasmOpt()
	if wasmOpt == "" {
		fmt.Fprintln(os.Stderr, "warning: wasm-opt not found; skipping binaryen size optimization.")
		fmt.Fprintln(os.Stderr, "  install binaryen for a smaller worker.wasm: place it in .tools/binaryen/bin")
		fmt.Fprintln(os.Stderr, "  or put wasm-opt on $PATH (https://github.com/WebAssembly/binaryen);")
		fmt.Fprintln(os.Stderr, "  CI installs it and enforces the size gate.")
		return nil
	}
	tmp := wasmPath + ".opt"
	cmd := exec.Command(wasmOpt, "-"+level, "-o", tmp, wasmPath)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("wasm-opt -%s: %w", level, err)
	}
	return os.Rename(tmp, wasmPath)
}

// defaultMainPkg picks the package to build: ./examples/hello inside the
// framework checkout, otherwise the current module.
func defaultMainPkg() string {
	if dirExists("examples/hello") {
		return "./examples/hello"
	}
	if dirExists("cmd") && !fileExists("main.go") {
		return "./..."
	}
	return "."
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// locateTinygo finds a usable tinygo binary: a vendored .tools/tinygo/bin/tinygo
// (searched from cwd upward, so the CLI works from project subdirectories)
// first, then $PATH.
func locateTinygo() (string, error) {
	if wd, err := os.Getwd(); err == nil {
		for dir := wd; ; dir = filepath.Dir(dir) {
			candidate := filepath.Join(dir, ".tools", "tinygo", "bin", "tinygo")
			if p, err := exec.LookPath(candidate); err == nil {
				return p, nil
			}
			if dir == filepath.Dir(dir) {
				break // reached root
			}
		}
	}
	if p, err := exec.LookPath("tinygo"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("tinygo not found: install it or place it in .tools/tinygo/bin")
}

// buildWasm compiles mainPkg to outDir/worker.wasm with TinyGo, copies the
// matching wasm_exec.js, and writes the worker.js glue module.
func buildWasm(plan buildPlan) (string, error) {
	tinygo, err := locateTinygo()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(plan.outDir, 0o755); err != nil {
		return "", err
	}
	wasmPath := filepath.Join(plan.outDir, "worker.wasm")

	cmd := exec.Command(tinygo, append(plan.tinygoFlags(), "-o", wasmPath, plan.mainPkg)...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "GOMAXPROCS="+fmt.Sprint(runtime.NumCPU()))
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tinygo build: %w", err)
	}
	if err := runWasmOpt(plan, wasmPath); err != nil {
		return "", err
	}

	rootOut, err := exec.Command(tinygo, "env", "TINYGOROOT").Output()
	if err != nil {
		return "", fmt.Errorf("tinygo env TINYGOROOT: %w", err)
	}
	shimSrc := filepath.Join(trimSpaceBytes(rootOut), "targets", "wasm_exec.js")
	shimBytes, err := os.ReadFile(shimSrc)
	if err != nil {
		return "", fmt.Errorf("read wasm_exec.js: %w", err)
	}
	// Keep the repo-root copy in sync (worker.js imports it) and also place a
	// copy in outDir for standalone use.
	for _, dst := range []string{"wasm_exec.js", filepath.Join(plan.outDir, "wasm_exec.js")} {
		if err := os.WriteFile(dst, shimBytes, 0o644); err != nil {
			return "", err
		}
	}

	glue := workerJSGlue("dist/worker.wasm")
	gluePath := filepath.Join(plan.outDir, "worker.js")
	if err := os.WriteFile(gluePath, []byte(glue), 0o644); err != nil {
		return "", err
	}
	// The repo-root worker.js imports ./dist/worker.wasm and ./wasm_exec.js;
	// for non-default out/entry layouts the glue lives next to the wasm and
	// wrangler.jsonc's main should point at it.
	if plan.outDir == "dist" {
		if err := os.WriteFile("worker.js", []byte(workerJSGlue("dist/worker.wasm")), 0o644); err != nil {
			return "", err
		}
	}

	fmt.Println("built", wasmPath)
	fmt.Println("wrote", gluePath)
	return wasmPath, nil
}

func runWrangler(args ...string) error {
	wrangler, err := exec.LookPath("npx")
	if err != nil {
		return fmt.Errorf("npx not found: %w", err)
	}
	full := append([]string{"wrangler"}, args...)
	c := exec.Command(wrangler, full...)
	c.Stdout, c.Stderr, c.Stdin = os.Stdout, os.Stderr, os.Stdin
	return c.Run()
}

func trimSpaceBytes(b []byte) string {
	s := string(b)
	for len(s) > 0 && (s[0] == '\n' || s[0] == ' ' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
