.PHONY: test fmt check wasm wasm-test install-wasmbrowsertest dry-run

TINYGO ?= $(if $(wildcard .tools/tinygo/bin/tinygo),.tools/tinygo/bin/tinygo,tinygo)

test:
	go test ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './.tools/*')

check: fmt test wasm-test
	go vet ./...
	git diff --check

install-wasmbrowsertest:
	@command -v wasmbrowsertest >/dev/null 2>&1 || go install github.com/agnivade/wasmbrowsertest@latest

# Runs the Cloudflare bridge tests in a real browser via wasmbrowsertest.
# Skips automatically when no browser is available (e.g. minimal sandboxes).
wasm-test: install-wasmbrowsertest
	@if command -v google-chrome chromium chromium-browser >/dev/null 2>&1 \
		|| [ -n "$$(ls ~/.cache/ms-playwright 2>/dev/null)" ]; then \
		exec="rt=$$(command -v wasmbrowsertest)"; \
		[ -n "$$rt" ] || rt=$$(go env GOPATH)/bin/wasmbrowsertest; \
		GOOS=js GOARCH=wasm go test -exec "$$rt" ./runtime/...; \
	else \
		echo "wasm-test: no browser found, skipping"; \
	fi

wasm:
	mkdir -p dist
	$(TINYGO) build -target wasm -opt=z -no-debug -o dist/worker.wasm ./examples/hello
	cp $$($(TINYGO) env TINYGOROOT)/targets/wasm_exec.js wasm_exec.js

dry-run: wasm
	npx wrangler deploy --dry-run
