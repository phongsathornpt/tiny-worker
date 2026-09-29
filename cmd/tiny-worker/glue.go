package main

// workerJSGlue renders the ES-module glue that boots the Go wasm and bridges
// fetch requests to it. wasmPath is the module specifier of the compiled
// wasm (as imported from the repo root).
//
// Keep in sync with the repo-root worker.js; TestGlueMatchesRepoWorker pins
// them together.
func workerJSGlue(wasmPath string) string {
	return "import wasm from \"./" + wasmPath + "\";\n" +
		"import \"./wasm_exec.js\";\n" +
		"\n" +
		"const FALLBACK_MAX_BODY = 10 * 1024 * 1024;\n" +
		"\n" +
		"let go = new globalThis.Go();\n" +
		"let ready;\n" +
		"\n" +
		"function freshRuntime() {\n" +
		"  // Drop a dead or failed runtime so the next boot() starts clean.\n" +
		"  go = new globalThis.Go();\n" +
		"  ready = undefined;\n" +
		"}\n" +
		"\n" +
		"function boot() {\n" +
		"  if (ready) return ready;\n" +
		"  ready = WebAssembly.instantiate(wasm, go.importObject)\n" +
		"    .then((result) => {\n" +
		"      // Bundlers hand us a pre-compiled WebAssembly.Module (workerd), where\n" +
		"      // instantiate() resolves to the Instance itself. When given raw bytes\n" +
		"      // (tests, plain Node), it resolves to { module, instance }.\n" +
		"      const instance = result instanceof WebAssembly.Instance ? result : result.instance;\n" +
		"      if (!instance || !instance.exports) {\n" +
		"        throw new Error(\"tiny-worker: instantiate produced no instance\");\n" +
		"      }\n" +
		"      // Runs main() up to its event loop; only resolves if the program exits.\n" +
		"      const runP = go.run(instance);\n" +
		"      // Diagnostic for early exits: a bridge-less exit means the Go program\n" +
		"      // crashed during init (its panic text goes to runtime stdout, which is\n" +
		"      // not visible in worker logs).\n" +
		"      runP.then(\n" +
		"        (code) => console.error(\"tiny-worker: go program exited early, code=\", code,\n" +
		"          \"bridge=\", typeof globalThis.tinyWorkerFetch),\n" +
		"        () => {},\n" +
		"      );\n" +
		"      if (!globalThis.tinyWorkerFetch) throw new Error(\"tiny-worker bridge unavailable\");\n" +
		"    })\n" +
		"    .catch((err) => {\n" +
		"      // Never cache a failed boot: reset so the next request retries\n" +
		"      // instantiation instead of awaiting a rejected promise forever.\n" +
		"      freshRuntime();\n" +
		"      throw err;\n" +
		"    });\n" +
		"  return ready;\n" +
		"}\n" +
		"\n" +
		"function maxBody() {\n" +
		"  // Published by the Go bridge at Register; <= 0 disables the limit.\n" +
		"  const v = globalThis.tinyWorkerMaxBody;\n" +
		"  return typeof v === \"number\" ? v : FALLBACK_MAX_BODY;\n" +
		"}\n" +
		"\n" +
		"function tooLarge() {\n" +
		"  return new Response(\"payload too large\", { status: 413 });\n" +
		"}\n" +
		"\n" +
		"function serverError() {\n" +
		"  return new Response(\"internal server error\", { status: 500 });\n" +
		"}\n" +
		"\n" +
		"function toResponse(result) {\n" +
		"  if (!result) return serverError();\n" +
		"  try {\n" +
		"    return new Response(result.body, { status: result.status, headers: result.headers });\n" +
		"  } catch (err) {\n" +
		"    console.error(\"tiny-worker: invalid response from handler:\", err);\n" +
		"    return serverError();\n" +
		"  }\n" +
		"}\n" +
		"\n" +
		"async function handle(request) {\n" +
		"  await boot();\n" +
		"\n" +
		"  const limit = maxBody();\n" +
		"  if (limit > 0) {\n" +
		"    // Reject on declared size before buffering anything.\n" +
		"    const declared = Number(request.headers.get(\"content-length\") ?? 0);\n" +
		"    if (declared > limit) return tooLarge();\n" +
		"  }\n" +
		"\n" +
		"  const body = request.body ? new Uint8Array(await request.arrayBuffer()) : null;\n" +
		"  if (limit > 0 && body !== null && body.byteLength > limit) return tooLarge();\n" +
		"\n" +
		"  const payload = {\n" +
		"    method: request.method, url: request.url,\n" +
		"    headers: [...request.headers], body,\n" +
		"  };\n" +
		"\n" +
		"  let result;\n" +
		"  try {\n" +
		"    result = globalThis.tinyWorkerFetch(payload);\n" +
		"  } catch (err) {\n" +
		"    // The Go runtime died (fatal panic or exit that escaped recover()).\n" +
		"    // Rebuild it and retry this request once before giving up.\n" +
		"    console.error(\"tiny-worker: runtime died, restarting:\", err);\n" +
		"    freshRuntime();\n" +
		"    await boot();\n" +
		"    result = globalThis.tinyWorkerFetch(payload);\n" +
		"  }\n" +
		"  return toResponse(result);\n" +
		"}\n" +
		"\n" +
		"export default {\n" +
		"  async fetch(request) {\n" +
		"    try {\n" +
		"      return await handle(request);\n" +
		"    } catch (err) {\n" +
		"      console.error(\"tiny-worker: request failed:\", err);\n" +
		"      return serverError();\n" +
		"    }\n" +
		"  },\n" +
		"};\n" +
		""
}
