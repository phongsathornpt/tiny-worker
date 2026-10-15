# Agent guide

Use this file for repository-wide working rules. `README.md` is the guide to
the public API and development workflow; `Makefile` and `.github/workflows/`
are authoritative for commands and CI.

Run shell commands through `rtk` in this environment.

## Project boundaries

- Keep the root Go module dependency-light. `rest` and `queue` are opt-in
  packages; importing JSON or queue support into the core changes the Wasm
  dependency graph and bundle size.
- Preserve the fetch and queue bridge contracts across Go, JavaScript, and
  native stubs. Queue delivery is at-least-once: consumers fail fast, ack
  progress is cumulative, and producer sends flush through the invocation
  outbox. See the queue and runtime sections in `README.md` before changing
  these paths.
- The root module and `site/` are separate Go modules. Account for both when
  changing shared APIs or site code.

## Sources and generated files

- `cmd/tiny-worker/glue.go` is the source for the mirrored root `worker.js`.
  Keep them synchronized; `TestGlueMatchesRepoWorker` pins the output.
- Edit `site/views/pages.templ` and `site/sitegen/` sources, then use
  `make site-generate` to refresh generated templates and rendered fragments.
- CLI scaffolds are product code. Keep their Clean Architecture dependency
  direction strict: `internal/domain` owns business entities, domain errors,
  and repository contracts; transport DTOs stay in adapters; outer adapters
  implement domain contracts. Update templates in `cmd/tiny-worker/new.go` or
  `queue_scaffold.go` and keep scaffold tests aligned with generated structure.
- Treat toolchain outputs such as `wasm_exec.js` and files under `dist/` as
  generated artifacts. Change their source or build process, then regenerate.

## Verification

Choose checks that exercise the changed contract and report checks that were
skipped or not run:

- `rtk make test` runs native tests for the root module and `site/`.
- `rtk make wasm-test` covers Cloudflare bridge behavior under browser
  `js/wasm` when fetch or queue interop changes.
- `rtk make smoke` builds and exercises the TinyGo artifacts when runtime,
  glue, or deployed behavior changes.
- `rtk make size-check` measures bundle changes. First confirm the committed
  baselines exist: the size script creates a baseline when one is missing, so
  a successful run without those files does not enforce a threshold. At the
  current checkout, all three baseline files are absent. Treat size as a
  design constraint and refresh baselines only for intentional, reviewed
  growth.
- Use Wrangler dry runs for Worker entrypoint, binding, or config changes; see
  `.github/workflows/ci.yml` for the root and queue-example commands.
