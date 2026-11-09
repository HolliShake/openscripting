# Express-like for the Go VM: Roadmap to a Full Express Clone

This document lists everything still required to take `core_http.go` from an Express-style core to a full Express 4/5 clone. Items are grouped into phases, ordered by value and by dependency. Effort tags: **S** (under half a day), **M** (about a day), **L** (several days or VM work).

## 1. Current status

### Done

- [x] HTTP verbs: `get`, `post`, `put`, `delete`, `patch`, `head`, `options`, `all`
- [x] Multiple handlers per route
- [x] `use([path], ...handlers | subApp)` with prefix matching
- [x] Mounting an `Express`/`Router` instance under a prefix (snapshot at mount time)
- [x] `next()` and `next(err)`, plus a single global `app.onError(fn)`
- [x] Path params (`:id`) and `*` wildcard exposed as `req.params`
- [x] Request: `method`, `path`, `originalUrl`, `query` (raw), `queryParams`, `body`, `hostname`, `ip`, `headers`, `get()`, `json()`
- [x] Response: `status`, `sendStatus`, `send`, `json`, `set`/`header`, `get`, `type`, `redirect`, `end`
- [x] HEAD falls back to GET routes, and an unhandled chain returns 404
- [x] `Router` export, `listen`, `address`, `close`, and `fetch`

### Known limitations of what exists

- Handlers must be synchronous.
- The request body limit is hard-coded at 1 MB.
- Only the first value of repeated headers is kept.
- Responses are buffered in memory.
- Mounting copies layers, so later route additions to a router are ignored.

## 2. Ground rules for every phase

1. **VM thread vs listener goroutine.** Scripts only ever run on the VM thread. The listener goroutine must copy everything it needs into `httpIncoming` and never touch VM values.
2. **Compile-time work stays off the hot path.** Convert path patterns to compiled matchers at registration time, never per request.
3. **Every feature needs tests** in both Go (unit, no VM) and script form (end to end through a real listener on `127.0.0.1:0`).
4. **Match Express semantics, not just names.** When unsure, check Express's behavior and its test suite, and treat divergences as bugs unless documented.

## 3. Phase 1: Core semantics (highest priority)

These affect how every other feature behaves, so do them first.

### 3.1 Async handlers (L, needs VM work)

- [ ] Allow `async` functions as handlers (remove the `Async` rejection in `httpValidTarget`).
- [ ] Suspend the request when a handler returns a pending Promise. Resume the chain when it settles.
- [ ] Reject means `next(err)`, so a rejected handler Promise routes to the error handler.
- [ ] Keep the request alive across event-loop turns: `httpIncoming.done` must not be closed until the chain finishes.
- [ ] Add a per-request timeout so a never-settling Promise does not leak the connection.
- [ ] Restructure `serveHTTPIncoming` into a resumable state machine (`chain index`, `error mode`, `response data`).

**Done when:** `app.get("/", async (req, res) => { res.json(await fetch(...).json()) })` works, and a thrown error inside an async handler reaches `onError`.

### 3.2 Real error-handling middleware (M)

- [ ] Detect error handlers by arity (4 parameters) so they can sit anywhere in the stack, not only via `onError`.
- [ ] While in error mode, skip normal layers and run only error layers.
- [ ] Catch runtime errors thrown by script handlers and convert them to `next(err)`.
- [ ] Keep `app.onError` as a convenience that registers a trailing error layer.
- [ ] Default error handler: 500, or `err.status`/`err.statusCode` when present, and hide the message in production mode.

### 3.3 Full `next()` semantics (S)

- [ ] `next("route")` skips the remaining handlers of the current route.
- [ ] `next("router")` exits the current router and returns to the parent.
- [ ] Calling `next()` twice, or sending after `next()`, should be detected and ignored or logged.

### 3.4 Live router mounting (M)

- [ ] Replace layer-copying with a real tree: a mounted router is one layer that delegates to the child's stack at request time.
- [ ] Track `req.baseUrl` and strip the mount prefix before child matching.
- [ ] Restore `req.url`/`req.path` when leaving the child router.
- [ ] Support `mergeParams` and mounting on parameterized prefixes (`/users/:id/posts`).
- [ ] Fire the `mount` event on the child (see 6.4).

### 3.5 Route chaining and `app.param` (M)

- [ ] `app.route(path)` returns a `Route` with `.get()/.post()/.all()/...` chainable (this replaces the current 4-arg `route()` or coexists via arity).
- [ ] `app.param(name, fn)` and `router.param(name, fn)` with the `(req, res, next, value, name)` signature.
- [ ] Run each param callback once per request per value.

### 3.6 Path pattern engine (M)

- [ ] Optional params (`:id?`), repeated params, and param constraints (`:id(\\d+)`).
- [ ] Wildcards (`*`) at any position, not only the tail.
- [ ] Regex routes (`/regex/`) if the VM has a regex type, with capture groups in `req.params[0]`.
- [ ] Array of paths (`["/a", "/b"]`).
- [ ] `case sensitive routing` and `strict routing` settings (see 6.1).
- [ ] Param decoding rules: decode once, and return a clean error on malformed percent-encoding.
- [ ] Consider porting `path-to-regexp` semantics and compiling to a segment matcher or regex once at registration.

### 3.7 Automatic OPTIONS and 405 (S)

- [ ] Auto-respond to `OPTIONS` with an `Allow` header built from registered methods for that path.
- [ ] Optionally answer 405 with `Allow` when the path matches but the method does not (Express 5 behavior is a 404, so make this a setting).

## 4. Phase 2: Request and response completeness

### 4.1 Request object

- [ ] `req.params` per layer, with parent params merged when `mergeParams` is on
- [ ] `req.baseUrl`, `req.url`, `req.originalUrl` (kept consistent with mounting)
- [ ] `req.protocol`, `req.secure`, `req.hostname` (honoring `trust proxy`), `req.subdomains`, `req.ips`
- [ ] `req.xhr`
- [ ] `req.route` (the matched route)
- [ ] `req.app` and `req.res` back-references
- [ ] `req.accepts()`, `acceptsCharsets()`, `acceptsEncodings()`, `acceptsLanguages()` (see 4.4)
- [ ] `req.is(type)` for content-type matching
- [ ] `req.range()`
- [ ] `req.fresh` and `req.stale` (see 4.5)
- [ ] Multi-value headers: keep all values (`req.headers` as array when repeated, or `req.getAll(name)`)
- [ ] Parsed `req.query` as an object (Express behavior). Keep the raw string available as `req.rawQuery`, and migrate existing scripts. A `query parser` setting should cover `simple`/`extended`/custom.
- [ ] Nested and array query syntax (`a[b]=1`, `a=1&a=2`) under the `extended` parser.

### 4.2 Body parsing (M)

- [ ] `express.json({ limit, strict, type })`
- [ ] `express.urlencoded({ extended, limit })`
- [ ] `express.text()` and `express.raw()`
- [ ] Configurable body size limit (replace the hard-coded 1 MB), returning 413
- [ ] Charset handling and `Content-Encoding` (gzip/deflate) decoding
- [ ] Populate `req.body` as a parsed value only when a parser matched, and leave it undefined otherwise. Today `req.body` is the raw string, so keep it as `req.rawBody` for compatibility.
- [ ] Error mapping: 400 for malformed JSON, 415 for unsupported media type, 413 for too large.
- [ ] Multipart/form-data (file uploads) as a separate module (see 7.2).

### 4.3 Response object

- [ ] `res.append(field, value)`
- [ ] `res.location(url)`
- [ ] `res.links(map)`
- [ ] `res.vary(field)`
- [ ] `res.cookie(name, value, opts)` and `res.clearCookie(name, opts)` (see 5.1)
- [ ] `res.attachment([filename])`
- [ ] `res.sendFile(path, opts)` and `res.download(path, name)` (see 5.2)
- [ ] `res.format({ ... })` (content negotiation)
- [ ] `res.jsonp()` (with the `jsonp callback name` setting)
- [ ] `res.render(view, locals)` (see 6.3)
- [ ] `res.locals` and `res.headersSent`
- [ ] `res.send` type handling for Buffer, string, object, and number/status quirks
- [ ] `res.json` honoring `json spaces`, `json replacer`, and `json escape` settings
- [ ] Automatic `Content-Length`, `ETag` (see 4.5), and `Content-Type` charset
- [ ] Guard against double sends (second `send`/`json` should error or be ignored)
- [ ] `res.status().send()` chaining should keep `sent` correct when `status()` is called after `send()`.

### 4.4 Content negotiation (S)

- [ ] Implement Accept parsing with q-values (types, languages, encodings, charsets).
- [ ] Wire `req.accepts*` and `res.format`.
- [ ] Reply 406 from `res.format` when nothing matches and no `default` is given.

### 4.5 Caching headers (M)

- [ ] Weak and strong ETag generation (setting: `etag`)
- [ ] `If-None-Match` and `If-Modified-Since` handling, producing 304 responses
- [ ] `req.fresh` and `req.stale`
- [ ] `Last-Modified` for `sendFile`

## 5. Phase 3: Features people expect from Express apps

### 5.1 Cookies (M)

- [ ] Parse the `Cookie` header into `req.cookies`.
- [ ] `res.cookie` options: `maxAge`, `expires`, `path`, `domain`, `secure`, `httpOnly`, `sameSite`, `signed`.
- [ ] Signed cookies with HMAC and `req.signedCookies` (needs a secret, so `cookieParser(secret)`).
- [ ] Multiple `Set-Cookie` headers (needs multi-value response headers, see 7.4).

### 5.2 Static files and file sending (M-L)

- [ ] `express.static(root, opts)` with `index`, `dotfiles`, `extensions`, `maxAge`, `immutable`, `redirect`, `fallthrough`, `setHeaders`.
- [ ] Path traversal protection (reject `..` and encoded variants, confirm the resolved path stays under root).
- [ ] MIME type lookup by extension.
- [ ] Range requests (`206`, `Content-Range`) and `If-Range`.
- [ ] `res.sendFile` and `res.download`, including error callback and `Content-Disposition`.
- [ ] Stream file contents instead of loading them into memory (needs 7.3).

### 5.3 Sessions (out of core, but required for parity with common apps)

- [ ] Session middleware with a pluggable store interface (memory store as default).
- [ ] Session cookie handling and `req.session`.
- [ ] Optional: ship as a separate module and not part of the core.

### 5.4 Common middleware parity (optional modules)

- [ ] CORS
- [ ] Compression (gzip/deflate/brotli)
- [ ] Logger (morgan-style)
- [ ] Rate limiting
- [ ] Security headers (helmet-style)
- [ ] Method override
- [ ] Response time

These are third-party packages in the Express ecosystem. Decide whether each is in scope for "full clone" or is left to scripts once the core is complete.

## 6. Phase 4: App-level features

### 6.1 Settings (S-M)

- [ ] `app.set(name, value)`, `app.get(name)`, `app.enable/disable/enabled/disabled`
- [ ] Settings to honor: `case sensitive routing`, `strict routing`, `etag`, `json spaces`, `json replacer`, `json escape`, `jsonp callback name`, `query parser`, `trust proxy`, `subdomain offset`, `x-powered-by`, `env`, `views`, `view engine`, `view cache`
- [ ] Inheritance: a mounted child app inherits parent settings unless overridden.
- [ ] `app.get(path, ...handlers)` vs `app.get(setting)` dispatch by arity (Express behavior).

### 6.2 `app.locals` and `res.locals` (S)

- [ ] `app.locals` available to all templates and handlers.
- [ ] `res.locals` is per request and merged into render context.

### 6.3 View engines (M)

- [ ] `app.engine(ext, fn)` registration.
- [ ] `res.render` resolving views by `views` directory and `view engine` extension.
- [ ] View cache.
- [ ] Decide on a built-in minimal template engine, or leave this to script modules.

### 6.4 App events and mounting metadata (S)

- [ ] `mount` event emitted on the child with the parent.
- [ ] `app.path()` returning the canonical mount path.
- [ ] `app.parent`.

### 6.5 Lifecycle and server options (M)

- [ ] HTTPS support (`listenTLS(addr, cert, key)` or an options object).
- [ ] HTTP/2 if needed.
- [ ] `listen` callback or Promise, plus options: `host`, `port`, `backlog`.
- [ ] Graceful shutdown: stop accepting, drain in-flight requests, then close (currently `close()` is abrupt).
- [ ] Timeouts: read header, read, write, idle, and handler timeout.
- [ ] Configurable max header bytes.
- [ ] Trust-proxy aware `req.ip`/`req.protocol`/`req.hostname`.

## 7. Phase 5: Platform and runtime work

### 7.1 Streaming request bodies (L)

- [ ] Expose the body as a stream and not only a pre-read string, so large uploads do not need to be buffered.
- [ ] Keep the buffered path for parsers that need the whole body.

### 7.2 Multipart and file uploads (L)

- [ ] Multipart parser, with field and file limits.
- [ ] Temp-file storage and an in-memory option.
- [ ] `req.files` and `req.body` fields.

### 7.3 Streaming responses (L)

- [ ] `res.write(chunk)` and `res.end()` with chunked transfer encoding.
- [ ] `res.flush`, plus Server-Sent Events as a smoke test.
- [ ] Backpressure handling.
- [ ] `res.pipe`-style helpers for file streaming.
- [ ] Connection close detection (`req.on("close")`) so handlers can stop work early.

### 7.4 Multi-value headers (S)

- [ ] Support repeated response headers (`Set-Cookie`, `Link`, `Vary`) with `Add` and not `Set`.
- [ ] Expose all request header values.

### 7.5 WebSockets and upgrades (optional)

- [ ] `Upgrade` handling and an integration point for a WebSocket library.

## 8. Testing and quality

- [ ] Go unit tests for: `matchSegments`, path compiler, negotiation parser, ETag/freshness, cookie parse/serialize, range parser.
- [ ] Script-level integration tests per feature, hitting a real listener.
- [ ] Port the relevant parts of the Express test suite and track the pass rate.
- [ ] Race detector (`go test -race`) in CI, since the listener and VM threads share state.
- [ ] Fuzz tests for the path matcher, query parser, body parsers and header parsing.
- [ ] Benchmarks: route lookup with 1k routes, middleware chain depth, body parsing throughput.
- [ ] Load test for leaked goroutines and connections, especially around async handlers and `close()`.

## 9. Performance and safety

- [ ] Index routes by method and first static segment so lookup is not a linear scan of all layers.
- [ ] Precompute static-route responses (already partly done by the static fast path).
- [ ] Pool request and response objects if allocation shows up in profiles.
- [ ] Enforce limits on header count, URL length, query parameter count, and body size.
- [ ] Never construct filesystem paths from user input without normalization and a root check.
- [ ] Do not leak internal error messages when `env` is `production`.
- [ ] Review the `unsafe.String` usages: request bodies and fetch responses alias byte slices, so confirm nothing mutates them afterward.

## 10. Suggested implementation order

1. Async handlers (3.1), because many later features depend on them.
2. Error middleware by arity (3.2) and full `next()` (3.3).
3. Live router mounting (3.4), `app.route()` and `app.param` (3.5).
4. Path pattern engine (3.6) and auto OPTIONS (3.7).
5. Body parsers (4.2) and parsed `req.query` (4.1).
6. Multi-value headers (7.4), then cookies (5.1).
7. Settings, `locals` and events (6.1, 6.2, 6.4).
8. Content negotiation and caching headers (4.4, 4.5).
9. Static files and `sendFile` (5.2), then streaming responses (7.3).
10. Everything else: views, HTTPS, uploads, sessions, and the optional middleware modules.

## 11. Compatibility notes and decisions to make

- **Express 4 vs 5.** Decide the target up front. Express 5 changes path syntax (`path-to-regexp` v8), makes `req.query` a getter, rejects on async errors natively, and drops several deprecated methods.
- **`req.query` and `req.body` types.** The current implementation exposes raw strings. Moving to Express semantics (parsed objects) breaks existing scripts, so either version the module or add `rawQuery`/`rawBody` first.
- **Route-level `route()` signature.** The existing `route(method, path, status, body)` conflicts with Express's `app.route(path)`. Pick one, or dispatch by argument count.
- **Scope of "full clone".** Core Express ships only routing, `static`, `json`, `urlencoded`, `raw` and `text`. Sessions, CORS and similar are separate packages. Decide whether to include them.

## 12. Definition of done

The clone is "full" when:

1. Every method and property in the Express API reference (application, request, response, router) exists or is explicitly listed as unsupported with a reason.
2. The ported Express test suite passes, apart from documented divergences.
3. A typical Express app (routers, body parsing, cookies, static files, error handling, async handlers) runs unmodified apart from import syntax.
4. The race detector and load tests pass with no leaked goroutines or connections.
