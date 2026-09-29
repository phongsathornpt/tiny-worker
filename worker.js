import wasm from "./dist/worker.wasm";
import "./wasm_exec.js";

const FALLBACK_MAX_BODY = 10 * 1024 * 1024;

let go = new globalThis.Go();
let ready;

function freshRuntime() {
  // Drop a dead or failed runtime so the next boot() starts clean.
  go = new globalThis.Go();
  ready = undefined;
}

function boot() {
  if (ready) return ready;
  ready = WebAssembly.instantiate(wasm, go.importObject)
    .then((result) => {
      // Bundlers hand us a pre-compiled WebAssembly.Module (workerd), where
      // instantiate() resolves to the Instance itself. When given raw bytes
      // (tests, plain Node), it resolves to { module, instance }.
      const instance = result instanceof WebAssembly.Instance ? result : result.instance;
      if (!instance || !instance.exports) {
        throw new Error("tiny-worker: instantiate produced no instance");
      }
      // Runs main() up to its event loop; only resolves if the program exits.
      const runP = go.run(instance);
      // Diagnostic for early exits: a bridge-less exit means the Go program
      // crashed during init (its panic text goes to runtime stdout, which is
      // not visible in worker logs).
      runP.then(
        (code) => console.error("tiny-worker: go program exited early, code=", code,
          "bridge=", typeof globalThis.tinyWorkerFetch),
        () => {},
      );
      if (!globalThis.tinyWorkerFetch) throw new Error("tiny-worker bridge unavailable");
    })
    .catch((err) => {
      // Never cache a failed boot: reset so the next request retries
      // instantiation instead of awaiting a rejected promise forever.
      freshRuntime();
      throw err;
    });
  return ready;
}

function maxBody() {
  // Published by the Go bridge at Register; <= 0 disables the limit.
  const v = globalThis.tinyWorkerMaxBody;
  return typeof v === "number" ? v : FALLBACK_MAX_BODY;
}

function tooLarge() {
  return new Response("payload too large", { status: 413 });
}

function serverError() {
  return new Response("internal server error", { status: 500 });
}

function toResponse(result) {
  if (!result) return serverError();
  try {
    return new Response(result.body, { status: result.status, headers: result.headers });
  } catch (err) {
    console.error("tiny-worker: invalid response from handler:", err);
    return serverError();
  }
}

async function handle(request) {
  await boot();

  const limit = maxBody();
  if (limit > 0) {
    // Reject on declared size before buffering anything.
    const declared = Number(request.headers.get("content-length") ?? 0);
    if (declared > limit) return tooLarge();
  }

  const body = request.body ? new Uint8Array(await request.arrayBuffer()) : null;
  if (limit > 0 && body !== null && body.byteLength > limit) return tooLarge();

  const payload = {
    method: request.method, url: request.url,
    headers: [...request.headers], body,
  };

  let result;
  try {
    result = globalThis.tinyWorkerFetch(payload);
  } catch (err) {
    // The Go runtime died (fatal panic or exit that escaped recover()).
    // Rebuild it and retry this request once before giving up.
    console.error("tiny-worker: runtime died, restarting:", err);
    freshRuntime();
    await boot();
    result = globalThis.tinyWorkerFetch(payload);
  }
  return toResponse(result);
}

export default {
  async fetch(request) {
    try {
      return await handle(request);
    } catch (err) {
      console.error("tiny-worker: request failed:", err);
      return serverError();
    }
  },
};
