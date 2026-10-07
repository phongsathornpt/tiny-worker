package main

// workerJSGlue renders the ES-module glue that boots the Go wasm, bridges
// fetch requests to it, and drives the queue consumer/producer path:
// batches enter via tinyWorkerQueueConsume with a durable ack ledger
// (tinyWorkerQueueAck), producer sends flush as awaited sendBatch calls,
// and a queue-path runtime death rebuilds and rethrows so Cloudflare
// redelivers only the unacked tail.
//
// wasmPath is the module specifier of the compiled wasm (as imported from
// the repo root).
//
// Keep in sync with the repo-root worker.js; TestGlueMatchesRepoWorker
// pins them together.
func workerJSGlue(wasmPath string) string {
	body := `import wasm from "./` + wasmPath + `";
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

async function handle(request, env) {
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
    headers: [...request.headers], body, env,
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

// ---- queue path ----

// Durable ack ledger, keyed by invocation id. tinyWorkerQueueAck (the Go
// bridge, on every Batch.AckThrough) appends here while the batch runs, so
// progress survives a mid-batch trap: on the rebuild-and-rethrow path the
// worker acks exactly the recorded prefix and lets Cloudflare redeliver
// the tail. workerd serializes invocations per isolate, so one entry at a
// time is live; the key exists to make that assumption explicit.
const ackLedger = new Map();
let currentInvocationId = null;

// Published for the Go bridge: one int per watermark advance (the count
// of acked messages), recorded durably while the batch is still running.
globalThis.tinyWorkerQueueAck = (watermark) => {
  if (currentInvocationId !== null) ackLedger.set(currentInvocationId, watermark);
};

function drainAcks(invocationId) {
  const watermark = ackLedger.get(invocationId) ?? 0;
  ackLedger.delete(invocationId);
  return watermark;
}

async function flushOutbox(outbox, env) {
  if (!outbox || !outbox.count) return;
  const queues = outbox.queues ?? {};
  await Promise.all(
    Object.entries(queues).map(async ([name, messages]) => {
      const binding = env?.[name];
      if (!binding || typeof binding.sendBatch !== "function") {
        throw new Error('tiny-worker: queue "' + name + '" is not bound in env (check wrangler.jsonc)');
      }
      for (let i = 0; i < messages.length; i += 100) {
        const chunk = messages.slice(i, i + 100);
        // One delay per chunk (sendBatch's second arg): the max requested.
        const delay = Math.max(0, ...chunk.map((m) => m.delaySeconds ?? 0));
        await binding.sendBatch(chunk, delay);
      }
    }),
  );
}

async function handleQueue(batch, env, ctx) {
  await boot();

  const invocationId = "q" + ++queueSeq;
  const messages = (batch.messages ?? []).map((m) => ({
    id: m.id,
    body: m.body ? new Uint8Array(m.body) : new Uint8Array(0),
    contentType: m.contentType,
    timestamp: typeof m.timestamp === "number" ? m.timestamp : Date.parse(m.timestamp) || 0,
    attempts: m.attempts,
  }));

  const payload = {
    queue: batch.queue,
    env, messages, invocationId,
  };

  let result;
  currentInvocationId = invocationId;
  try {
    result = globalThis.tinyWorkerQueueConsume(payload);
  } catch (err) {
    // Runtime died mid-batch (trap). Rebuild, ack the recorded prefix,
    // rethrow so Cloudflare redelivers only the tail.
    console.error("tiny-worker: runtime died during batch, rebuilding:", err);
    freshRuntime();
    await boot();
    const acked = drainAcks(invocationId);
    for (let i = 0; i < acked; i++) {
      batch.messages[i]?.ack();
    }
    throw err;
  } finally {
    currentInvocationId = null;
  }

  // The ledger holds the per-message acks recorded during the run; the
  // result's watermark is derived from the same AckThrough calls. Max of
  // the two for safety, then ack through it.
  const finalWatermark = Math.max(drainAcks(invocationId), result.watermark ?? 0);
  for (let i = 0; i < finalWatermark; i++) {
    batch.messages[i]?.ack();
  }

  // Chained sends (a consumer that produces) flush before the batch
  // resolves; a failure fails the batch so the unacked tail (and the
  // sends) are reapplied on redelivery.
  await flushOutbox(result.outbox, env);

  if (result.error) {
    const err = new Error(result.error);
    if ((result.delay ?? 0) > 0) {
      // Apply the handler's requested delay to the unacked tail via the
      // queue's retry action before surfacing the failure.
      for (let i = finalWatermark; i < messages.length; i++) {
        batch.messages[i]?.retry({ delaySeconds: result.delay });
      }
    }
    throw err;
  }
}

let queueSeq = 0;

export default {
  async fetch(request, env, ctx) {
    try {
      return await handle(request, env, ctx);
    } catch (err) {
      console.error("tiny-worker: request failed:", err);
      return serverError();
    }
  },
  async queue(batch, env, ctx) {
    // Queue batches bypass MaxBodyBytes: Queues caps message size itself,
    // so the fetch-path 413 guard must not eat deliveries.
    try {
      await handleQueue(batch, env, ctx);
    } catch (err) {
      console.error("tiny-worker: batch failed:", err);
      throw err;
    }
  },
};
`
	return body
}
