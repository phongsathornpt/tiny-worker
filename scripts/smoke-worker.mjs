// smoke-worker.mjs boots a built TinyGo artifact the way worker.js does for
// raw bytes and exercises it over the bridge. The size gate proves the wasm is
// small; this proves it works — wasm-opt post-processing, the -panic=trap
// default, and (for the REST profile) the whole binding/validation/envelope
// contract are otherwise untested (wasmbrowsertest covers the stock-Go js/wasm
// build, not TinyGo's output).
//
// Panic semantics pinned by the hello profile: under TinyGo a panic is *not*
// recoverable in Go (neither -panic=trap nor -panic=print unwinds), so it
// surfaces to JS as a thrown RuntimeError. worker.js catches that, rebuilds the
// runtime, and retries — the isolate survives. The profile asserts both halves.
//
// Usage: node scripts/smoke-worker.mjs [wasm] [wasm_exec.js] [profile]
//   profile: hello (default) | rest
// Skips (exit 0) when the artifacts are absent.
import { readFileSync, existsSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";

const wasmPath = resolve(process.argv[2] ?? "dist/worker.wasm");
const execPath = resolve(process.argv[3] ?? "wasm_exec.js");
const profile = process.argv[4] ?? "hello";

for (const p of [wasmPath, execPath]) {
  if (!existsSync(p)) {
    console.log(`smoke: ${p} not found, skipping (run make wasm first)`);
    process.exit(0);
  }
}

// wasm_exec.js is a plain script that installs globalThis.Go.
await import(pathToFileURL(execPath).href);

const decoder = new TextDecoder();
const failures = [];
function check(name, cond, detail = "") {
  if (cond) {
    console.log(`smoke: ok   ${name}`);
  } else {
    console.log(`smoke: FAIL ${name}${detail ? ` — ${detail}` : ""}`);
    failures.push(name);
  }
}

// Mirrors worker.js's boot(): fresh Go runtime around the compiled bytes.
async function bootRuntime() {
  const go = new globalThis.Go();
  const { instance } = await WebAssembly.instantiate(readFileSync(wasmPath), go.importObject);
  go.run(instance);
  return go;
}

function call(method, url, body = null, headers = []) {
  const payload = { method, url, headers, body };
  const res = globalThis.tinyWorkerFetch(payload);
  return {
    status: res.status,
    headers: res.headers ?? [],
    body: res.body ? decoder.decode(res.body) : "",
    json() {
      try {
        return JSON.parse(this.body);
      } catch {
        return null;
      }
    },
  };
}

const headerValue = (res, name) => {
  for (const pair of res.headers) {
    const [k, v] = Array.isArray(pair) ? pair : [pair[0], pair[1]];
    if (String(k).toLowerCase() === name.toLowerCase()) return v;
  }
  return "";
};

const post = (path, obj) =>
  call("POST", `https://smoke.test${path}`, new TextEncoder().encode(JSON.stringify(obj)));

await bootRuntime();
check("bridge installed", typeof globalThis.tinyWorkerFetch === "function");
if (typeof globalThis.tinyWorkerFetch !== "function") {
  console.log("smoke: bridge unavailable, aborting");
  process.exit(1);
}

if (profile === "rest") {
  // 1. Create: typed handler binds, validates, renders 201 JSON.
  const created = post("/users", { name: "Ada", email: "ada@example.com", handle: "ada", tags: ["x"] });
  check("POST /users -> 201", created.status === 201, `got ${created.status}`);
  check("POST /users content-type", headerValue(created, "content-type").includes("application/json"), headerValue(created, "content-type"));
  check("POST /users Location header", headerValue(created, "location") === "/users/1", headerValue(created, "location"));
  const user = created.json();
  check("POST /users body has id+name", user && user.id === 1 && user.name === "Ada", created.body);

  // 2. Validation failure: 422 envelope with a field-scoped detail.
  const invalid = post("/users", { name: "Ada", email: "not-an-email", handle: "ada" });
  check("invalid email -> 422", invalid.status === 422, `got ${invalid.status}`);
  const invalidBody = invalid.json();
  check("422 envelope code", invalidBody?.error?.code === "validation_failed", invalid.body);
  check(
    "422 details name the field",
    invalidBody?.error?.details?.some((d) => d.field === "email" && d.code === "email"),
    invalid.body,
  );

  // 3. Custom Validatable rule surfaced through the same envelope.
  const reserved = post("/users", { name: "ADMIN", email: "a@example.com", handle: "a" });
  check("reserved name -> 422", reserved.status === 422, `got ${reserved.status}`);
  check(
    "reserved detail code",
    reserved.json()?.error?.details?.some((d) => d.code === "reserved"),
    reserved.body,
  );

  // 4. Unknown field is strict 400, not a silent drop.
  const unknown = post("/users", { name: "Ada", email: "ada@example.com", handle: "ada", nmae: "typo" });
  check("unknown field -> 400", unknown.status === 400, `got ${unknown.status}`);
  check("unknown field code", unknown.json()?.error?.code === "invalid_json", unknown.body);
  check(
    "unknown field detail names it",
    unknown.json()?.error?.details?.some((d) => d.field === "nmae"),
    unknown.body,
  );

  // 5. Malformed JSON -> 400 invalid_json.
  const malformed = call("POST", "https://smoke.test/users", new TextEncoder().encode("{not json"));
  check("malformed JSON -> 400", malformed.status === 400, `got ${malformed.status}`);
  check("malformed code", malformed.json()?.error?.code === "invalid_json", malformed.body);

  // 6. Query binding + coercion + slice params.
  const listed = call("GET", "https://smoke.test/users?limit=1&tag=x");
  check("GET /users?limit=1 -> 200", listed.status === 200, `got ${listed.status}`);
  check("limit coerced to one item", Array.isArray(listed.json()) && listed.json().length === 1, listed.body);
  const filteredOut = call("GET", "https://smoke.test/users?tag=nope");
  check("GET /users?tag=nope filters", filteredOut.json()?.length === 0, filteredOut.body);
  const badQuery = call("GET", "https://smoke.test/users?limit=abc");
  check("bad query value -> 400", badQuery.status === 400, `got ${badQuery.status}`);

  // 7. Path params: fetch, coercion failure, and the 404 envelope.
  const fetched = call("GET", "https://smoke.test/users/1");
  check("GET /users/1 -> 200", fetched.status === 200 && fetched.json()?.id === 1, fetched.body);
  const badPath = call("GET", "https://smoke.test/users/abc");
  check("GET /users/abc -> 400", badPath.status === 400, `got ${badPath.status}`);
  check("bad path code", badPath.json()?.error?.code === "bad_request", badPath.body);
  const missing = call("GET", "https://smoke.test/users/999");
  check("GET /users/999 -> 404", missing.status === 404, `got ${missing.status}`);
  check("404 envelope code", missing.json()?.error?.code === "not_found", missing.body);

  // 8. PUT carries path param + body in one struct.
  const put = call(
    "PUT",
    "https://smoke.test/users/1",
    new TextEncoder().encode(JSON.stringify({ name: "Ada L", email: "ada@example.com" })),
  );
  check("PUT /users/1 -> 200", put.status === 200 && put.json()?.name === "Ada L", put.body);

  // 9. DELETE -> 204, then gone.
  const deleted = call("DELETE", "https://smoke.test/users/1");
  check("DELETE /users/1 -> 204", deleted.status === 204, `got ${deleted.status}`);
  check("DELETE has empty body", deleted.body === "", deleted.body);
  check("deleted user is gone", call("GET", "https://smoke.test/users/1").status === 404);

  // 10. Uniform errors: a routing miss is the same envelope.
  const noRoute = call("GET", "https://smoke.test/nope");
  check("unmatched route -> 404 envelope", noRoute.status === 404 && noRoute.json()?.error?.code === "not_found", noRoute.body);

  // 11. Primitives path still works (rest.OK on the index route).
  const index = call("GET", "https://smoke.test/");
  check("GET / -> 200 JSON", index.status === 200 && Array.isArray(index.json()?.routes), index.body);
} else {
  // hello profile: the framework itself.
  const hello = call("GET", "https://smoke.test/hello");
  check("GET /hello -> 200", hello.status === 200, `got ${hello.status}`);
  check("GET /hello body", hello.body === "hello", JSON.stringify(hello.body));

  const echo = call("POST", "https://smoke.test/echo", new TextEncoder().encode("ping"));
  check("POST /echo -> 200", echo.status === 200, `got ${echo.status}`);
  check("POST /echo echoes body", echo.body === "ping", JSON.stringify(echo.body));

  const missing = call("GET", "https://smoke.test/nope");
  check("GET /nope -> 404", missing.status === 404, `got ${missing.status}`);
  const wrongMethod = call("DELETE", "https://smoke.test/hello");
  check("DELETE /hello -> 405", wrongMethod.status === 405, `got ${wrongMethod.status}`);

  const who = call("GET", "https://smoke.test/whoami");
  check("GET /whoami -> 200", who.status === 200, `got ${who.status}`);
  const trace = call("GET", "https://smoke.test/trace");
  check("GET /trace -> 200", trace.status === 200, `got ${trace.status}`);

  let trapped = false;
  try {
    call("GET", "https://smoke.test/panic");
  } catch {
    trapped = true;
  }
  check("GET /panic traps the runtime (expected under TinyGo)", trapped);

  await bootRuntime();
  const afterRebuild = call("GET", "https://smoke.test/hello");
  check("rebuilt runtime serves /hello -> 200", afterRebuild.status === 200, `got ${afterRebuild.status}`);
}

if (failures.length > 0) {
  console.error(`smoke: ${failures.length} check(s) failed: ${failures.join(", ")}`);
  process.exit(1);
}
console.log(`smoke: all ${profile} checks passed`);
