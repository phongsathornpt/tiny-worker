#!/usr/bin/env bash
# Size gate for the built example worker: gzip the wasm and compare against a
# committed baseline. Growth beyond the ratchet percentage fails; shrinkage
# ratchets the baseline down. --write records a fresh baseline unconditionally.
#
# The baseline is recorded from a binaryen-optimized artifact, so environments
# without wasm-opt skip the comparison (loudly) rather than failing a build
# that simply couldn't run the optimizer; CI installs binaryen and enforces it.
#
# Usage: size-gate.sh [--write] <baseline-file> <ratchet-pct>
set -euo pipefail

write=0
if [ "${1:-}" = "--write" ]; then
  write=1
  shift
fi

baseline_file="${1:?usage: size-gate.sh [--write] <baseline-file> <ratchet-pct>}"
ratchet="${2:-2}"
wasm="${WASM:-dist/worker.wasm}"

[ -f "$wasm" ] || { echo "size-gate: $wasm not found (run make wasm first)" >&2; exit 1; }

wasm_opt="${WASM_OPT:-}"
[ -n "$wasm_opt" ] || wasm_opt="$(command -v wasm-opt 2>/dev/null || true)"
if [ -z "$wasm_opt" ]; then
  echo "size-gate: wasm-opt not found — skipping the size comparison."
  echo "  the baseline is recorded from a binaryen-optimized artifact; CI installs binaryen"
  echo "  (https://github.com/WebAssembly/binaryen) and enforces the gate."
  exit 0
fi

# -n keeps the gzip header free of the file name and mtime so the number is
# reproducible across machines.
actual=$(gzip -9 -n -c "$wasm" | wc -c | tr -d ' ')
raw=$(wc -c < "$wasm" | tr -d ' ')

if [ "$write" = 1 ] || [ ! -f "$baseline_file" ]; then
  mkdir -p "$(dirname "$baseline_file")"
  echo "$actual" > "$baseline_file"
  echo "size-gate: recorded baseline $actual bytes gzip ($raw bytes raw) in $baseline_file"
  exit 0
fi

baseline=$(tr -d '[:space:]' < "$baseline_file")
case "$baseline" in
  ''|*[!0-9]*) echo "size-gate: invalid baseline in $baseline_file: $baseline" >&2; exit 1 ;;
esac

max=$(( baseline + (baseline * ratchet) / 100 ))
echo "size-gate: gzip $actual bytes ($raw raw); baseline $baseline; max $max (+$ratchet%)"

if [ "$actual" -gt "$max" ]; then
  echo "size-gate: FAIL — gzipped wasm grew beyond the ratchet." >&2
  echo "  If the growth is intentional, refresh the baseline and commit it:" >&2
  echo "    make size-baseline" >&2
  exit 1
fi

if [ "$actual" -lt "$baseline" ]; then
  echo "size-gate: shrinkage — ratcheting baseline down to $actual bytes (commit $baseline_file)"
  echo "$actual" > "$baseline_file"
fi
