// Command tiny-worker is the companion CLI for the tiny-worker framework:
// it scaffolds projects, lists routes, and builds/deploys workers.
//
//	tiny-worker new myworker && cd myworker
//	tiny-worker build     # TinyGo -> dist/worker.wasm + worker.js glue
//	tiny-worker deploy    # wrangler deploy
package main

import (
	"errors"
	"fmt"
	"os"
)

const version = "0.1.0"

const usage = `tiny-worker is the companion CLI for the tiny-worker framework.

Usage:

  tiny-worker <command> [arguments]

Commands:

  new <dir>     Scaffold a new tiny-worker project
  routes        List routes registered via router.Handle (use in project root)
  build         Build worker.wasm and the worker.js glue into dist/
                flags: -main -out -panic -gc -opt (defaults: trap,
                conservative, Oz; pass -opt=none to skip wasm-opt)
  deploy        Build, then deploy with wrangler (same build flags)
  dev           Run wrangler dev against the built worker (same flags, -port)
  version       Print the CLI version

Flags:

  -h, help      Show this help

Use "tiny-worker <command> -h" for command-specific flags.`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	if err := dispatch(cmd, args); err != nil {
		if errors.Is(err, errHelpShown) {
			return // -h printed the usage already; nothing went wrong
		}
		fmt.Fprintf(os.Stderr, "tiny-worker: %v\n", err)
		os.Exit(1)
	}
}

func dispatch(cmd string, args []string) error {
	switch cmd {
	case "new":
		return cmdNew(args)
	case "routes":
		return cmdRoutes(args)
	case "build":
		return cmdBuild(args)
	case "deploy":
		return cmdDeploy(args)
	case "dev":
		return cmdDev(args)
	case "version", "-v", "--version":
		fmt.Println("tiny-worker CLI", version)
		return nil
	case "help", "-h", "--help":
		fmt.Println(usage)
		return nil
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s\n", cmd, usage)
		return errExit2
	}
}
