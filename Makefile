.PHONY: test fmt check wasm wasm-rest wasm-test install-wasmbrowsertest install-binaryen bench size-baseline size-check smoke dry-run deploy cli site-generate site-build site-dev site-deploy

TINYGO ?= $(if $(wildcard .tools/tinygo/bin/tinygo),.tools/tinygo/bin/tinygo,tinygo)
WASM_OPT ?= $(if $(wildcard .tools/binaryen/bin/wasm-opt),.tools/binaryen/bin/wasm-opt,$(shell command -v wasm-opt 2>/dev/null))
# Tracked (dist/ is gitignored) so CI and local runs compare one baseline.
SIZE_BASELINE_FILE ?= testdata/wasm-size-baseline.txt
REST_SIZE_BASELINE_FILE ?= testdata/wasm-size-rest-baseline.txt

test:
	go test ./...
	cd site && go test ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './.tools/*')

check: fmt test wasm-test
	go vet ./...
	git diff --check

# Native benchmarks (router dispatch + path parsing). wasm-side performance is
# proxied by the size gate; browser wall-clock is too noisy for CI.
bench:
	go test -bench . -benchmem -run '^$$' ./...

install-wasmbrowsertest:
	@command -v wasmbrowsertest >/dev/null 2>&1 || go install github.com/agnivade/wasmbrowsertest@latest

# Vendors binaryen's wasm-opt into .tools/binaryen/bin (picked up by the build
# and the size gate). Pinned so the size baseline stays comparable; x86_64
# Linux only, which matches the pinned CI runner.
BINARYEN_VERSION ?= version_133
install-binaryen:
	@mkdir -p .tools/binaryen/bin
	@curl -sSfL https://github.com/WebAssembly/binaryen/releases/download/$(BINARYEN_VERSION)/binaryen-$(BINARYEN_VERSION)-x86_64-linux.tar.gz | tar -xz
	@mv binaryen-$(BINARYEN_VERSION)/bin/wasm-opt .tools/binaryen/bin/
	@rm -rf binaryen-$(BINARYEN_VERSION)
	@.tools/binaryen/bin/wasm-opt --version

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

# Builds an example wasm the same way `tiny-worker build` does (its default
# flags), with binaryen wasm-opt when available.
# $(1) = output wasm path, $(2) = package to build
define build_wasm
	@mkdir -p $(dir $(1))
	$(TINYGO) build -target=wasm -opt=z -no-debug -panic=trap -gc=conservative -o $(1) $(2)
	@if [ -n "$(WASM_OPT)" ]; then \
		echo "wasm-opt -Oz $(1)"; \
		$(WASM_OPT) -Oz -o $(1).opt $(1) && mv $(1).opt $(1); \
	else \
		echo "warning: wasm-opt not found; skipping binaryen size optimization."; \
		echo "  run 'make install-binaryen' to vendor it into .tools/binaryen for a smaller $(1);"; \
	fi
endef

wasm:
	$(call build_wasm,dist/worker.wasm,./examples/hello)
	cp $$($(TINYGO) env TINYGOROOT)/targets/wasm_exec.js wasm_exec.js

# The REST example (rest package + stdlib encoding/json in the graph). Gated
# against its own baseline so the lean hello artifact stays measured separately.
wasm-rest:
	$(call build_wasm,dist/rest/worker.wasm,./examples/rest)

# Size gate: gzip each built example and compare against its committed
# baseline. Growth beyond the ratchet fails; shrinkage ratchets it down.
# Needs wasm-opt (binaryen) for a comparable artifact — without it the gate
# warns and skips, matching the build's warn+skip behavior.
SIZE_RATCHET ?= 2
size-check: wasm wasm-rest
	@WASM_OPT=$(WASM_OPT) bash ./scripts/size-gate.sh $(SIZE_BASELINE_FILE) $(SIZE_RATCHET)
	@WASM=dist/rest/worker.wasm WASM_OPT=$(WASM_OPT) bash ./scripts/size-gate.sh $(REST_SIZE_BASELINE_FILE) $(SIZE_RATCHET)

# Records the current gzipped sizes as the baselines (commit the result).
size-baseline: wasm wasm-rest
	@WASM_OPT=$(WASM_OPT) bash ./scripts/size-gate.sh --write $(SIZE_BASELINE_FILE) 0
	@WASM=dist/rest/worker.wasm WASM_OPT=$(WASM_OPT) bash ./scripts/size-gate.sh --write $(REST_SIZE_BASELINE_FILE) 0

# Boots the built artifacts in Node and exercises them: proves the wasm-opt'd
# output still works (the size gate alone cannot). Skips without node.
smoke: wasm wasm-rest
	@command -v node >/dev/null 2>&1 || { echo "smoke: node not found, skipping"; exit 0; }
	node scripts/smoke-worker.mjs dist/worker.wasm wasm_exec.js hello
	node scripts/smoke-worker.mjs dist/rest/worker.wasm wasm_exec.js rest

# Builds the wasm bundle and deploys the worker to Cloudflare.
dry-run: wasm
	npx wrangler deploy --dry-run

deploy: wasm
	npx wrangler deploy

# Installs the tiny-worker CLI into $(go env GOPATH)/bin (assumed to be on
# PATH). Override BIN_DIR to install elsewhere.
BIN_DIR ?= $(shell go env GOPATH)/bin
cli:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/tiny-worker ./cmd/tiny-worker
	@echo "installed $(BIN_DIR)/tiny-worker"

# The open-source project site uses templ for HTML and the framework's CLI for
# the Cloudflare Worker bundle. Wrangler publishes site/public as static assets.
site-generate:
	templ generate -path site
	cd site && go run ./sitegen

site-build: site-generate
	@mkdir -p .tools
	go build -o .tools/tiny-worker-site-cli ./cmd/tiny-worker
	cd site && ../.tools/tiny-worker-site-cli build

site-dev: site-build
	npx wrangler dev --config wrangler.site.jsonc --persist-to .wrangler/site-state

site-deploy: site-build
	npx wrangler deploy --config wrangler.site.jsonc
