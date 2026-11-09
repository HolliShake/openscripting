# Express-like for the Go VM: Roadmap to a Full Express Clone

This document lists everything still required to take `core_http.go` from an Express-style core to a full Express 4/5 clone. Items are grouped into phases, ordered by value and by dependency. Effort tags: **S** (under half a day), **M** (about a day), **L** (several days or VM work).

## 1. Current status

### Done

- [x] HTTP verbs: `get`, `post`, `put`, `delete`, `patch`, `head`, `options`, `all`
- [x] Multiple handlers per route
- [x] `use([path], ...handlers)` with prefix matching
- [x] Mounting an `Express`/`Router` instance under a prefix (snapshot at mount time; live delegation remains pending)
- [x] `next()` and `next(err)`, arity-based error middleware, and `app.onError(fn)` as a fallback
- [x] `next("route")` skips remaining handlers from the current route registration
- [x] Path params (`:id`) and `*` wildcard exposed as `req.params`
- [x] Request: `method`, `path`, `url`, `baseUrl`, `originalUrl`, parsed `query`, `rawQuery`, `body`, `hostname`, `ip`, protocol metadata, headers, `get()`, `getAll()`, `json()`, and `route`
- [x] Response: `status`, `sendStatus`, `send`, `json`, `jsonp`, `sendFile`, `download`, `attachment`, `set`/`header`, `append`, `get`, `type`, `links`, `vary`, `location`, `redirect`, `end`, `locals`, `headersSent`
- [x] HEAD falls back to GET routes, and an unhandled chain returns 404
- [x] `Router` export, `listen`, `address`, `close`, and `fetch`
- [x] Async route and error handlers suspend on Promises; rejections and synchronous throws enter error handling
- [x] Automatic `OPTIONS` responses include an `Allow` header, with `HEAD` included for `GET`

### Known limitations of what exists

- Responses, including file responses, are buffered in memory.
- Mounting copies layers, so later route additions to a router are ignored.

## 2. Ground rules for every phase

1. **VM thread vs listener goroutine.** Scripts only ever run on the VM thread. The listener goroutine must copy everything it needs into `httpIncoming` and never touch VM values.
2. **Compile-time work stays off the hot path.** Convert path patterns to compiled matchers at registration time, never per request.
3. **Every feature needs tests** in both Go (unit, no VM) and script form (end to end through a real listener on `127.0.0.1:0`).
4. **Match Express semantics, not just names.** When unsure, check Express's behavior and its test suite, and treat divergences as bugs unless documented.

## 3. Phase 1: Core semantics (highest priority)

These affect how every other feature behaves, so do them first.

### 3.1 Async handlers (L, needs VM work)

- [x] Allow `async` functions as handlers (remove the `Async` rejection in `httpValidTarget`).
- [x] Suspend the request when a handler returns a pending Promise. Resume the chain when it settles.
- [x] Reject means `next(err)`, so a rejected handler Promise routes to the error handler.
- [x] Keep the request alive across event-loop turns: `httpIncoming.done` must not be closed until the chain finishes.
- [x] Add a per-request timeout so a never-settling Promise does not leak the connection.
- [x] Restructure request dispatch into a resumable state machine (`chain index`, `error mode`, `response data`).

**Done when:** `app.get("/", async (req, res) => { res.json(await fetch(...).json()) })` works, and a thrown error inside an async handler reaches `onError`.

### 3.2 Real error-handling middleware (M)

- [x] Detect error handlers by arity (4 parameters) so they can sit anywhere in the stack, not only via `onError`.
- [x] While in error mode, skip normal layers and run only error layers.
- [x] Catch runtime errors thrown by script handlers and convert them to `next(err)`.
- [x] Keep `app.onError` as a trailing fallback for errors not handled by stack middleware.
- [x] Default error handler: 500, or `err.status`/`err.statusCode` when present, and hide the message in production mode.

### 3.3 Full `next()` semantics (S)

- [x] `next("route")` skips the remaining handlers of the current route registration.
- [ ] `next("router")` exits the current router and returns to the parent.
- [x] Repeated `next()` calls are ignored.
- [x] Sending after `next()` should be detected and ignored or logged.

### 3.4 Live router mounting (M)

- [ ] Replace layer-copying with a real tree: a mounted router is one layer that delegates to the child's stack at request time.
- [ ] Track `req.baseUrl` and strip the mount prefix before child matching.
- [ ] Restore `req.url`/`req.path` when leaving the child router.
- [ ] Support `mergeParams` and mounting on parameterized prefixes (`/users/:id/posts`).
- [ ] Fire the `mount` event on the child (see 6.4).

### 3.5 Route chaining and `app.param` (M)

- [x] `app.route(path)` returns a `Route` with `.get()/.post()/.all()/...` chainable (this replaces the current 4-arg `route()` or coexists via arity).
- [x] `app.param(name, fn)` and `router.param(name, fn)` with the `(req, res, next, value, name)` signature.
- [x] Run each param callback once per request per value.

### 3.6 Path pattern engine (M)

- [x] Optional params (`:id?`), repeated params (`:name+`, `:name*`), and param constraints (`:id(\\d+)`).
- [x] Wildcards (`*`) at any position, not only the tail.
- [ ] Regex routes (`/regex/`) if the VM has a regex type, with capture groups in `req.params[0]`.
- [x] Array of paths (`["/a", "/b"]`).
- [x] `case sensitive routing` and `strict routing` settings (see 6.1).
- [x] Param decoding rules: decode once; malformed percent-encoding is rejected with 400 before matching.
- [ ] Consider porting `path-to-regexp` semantics and compiling to a segment matcher or regex once at registration.

### 3.7 Automatic OPTIONS and 405 (S)

- [x] Auto-respond to `OPTIONS` with an `Allow` header built from registered methods for that path.
- [x] Optionally answer 405 with `Allow` when the path matches but the method does not (Express 5 behavior is a 404, so make this a setting).

## 4. Phase 2: Request and response completeness

### 4.1 Request object

- [x] `req.params` per matched layer; parent params merged with `mergeParams` remains pending
- [x] `req.baseUrl`, `req.url`, `req.originalUrl` (kept consistent with mounting; mount-aware values pending live router support)
- [x] `req.protocol`, `req.secure`, `req.hostname`, `req.ip`
- [x] `req.xhr`
- [x] `req.route` (the matched route)
- [x] `req.app` and `req.res` back-references
- [x] `req.accepts()`, `req.acceptsCharsets()`, `req.acceptsEncodings()`, `req.acceptsLanguages()` (see 4.4)
- [x] `req.is(type)` for content-type matching
- [x] `req.range()` with byte-range parsing and malformed/unsatisfiable result codes.
- [ ] `req.fresh` and `req.stale` (see 4.5)
- [x] Multi-value headers: keep all values (`req.headers` as array when repeated, or `req.getAll(name)`)
- [x] Parsed `req.query` as an object with repeated-key arrays. Keep the raw string available as `req.rawQuery`.
- [x] Nested and array query syntax (`a[b]=1`, `a[]=1`, `a=1&a=2`) under the `extended` parser.

### 4.2 Body parsing (M)

- [ ] `express.json({ limit, strict, type })`
- [ ] `express.urlencoded({ extended, limit })`
- [ ] `express.text()` and `express.raw()`
- [x] Configurable body size limit, returning 413
- [ ] Charset handling and `Content-Encoding` (gzip/deflate) decoding
- [ ] Populate `req.body` as a parsed value only when a parser matched, and leave it undefined otherwise. Today `req.body` is the raw string, so keep it as `req.rawBody` for compatibility.
- [ ] Error mapping: 400 for malformed JSON, 415 for unsupported media type, 413 for too large.
- [ ] Multipart/form-data (file uploads) as a separate module (see 7.2).

### 4.3 Response object

- [x] `res.append(field, value)`
- [x] `res.location(url)`
- [x] `res.links(map)`
- [x] `res.vary(field)`
- [x] `res.cookie(name, value, opts)` and `res.clearCookie(name, opts)` for unsigned cookies (see 5.1)
- [x] `res.attachment([filename])`
- [x] Buffered `res.sendFile(path, opts)` and `res.download(path, name)` with root containment
- [ ] `res.format({ ... })` (content negotiation)
- [x] `res.jsonp()` (with the configurable `jsonp callback name` setting)
- [ ] `res.render(view, locals)` (see 6.3)
- [x] `res.locals`
- [x] `res.headersSent`
- [ ] `res.send` type handling for Buffer, string, object, and number/status quirks
- [ ] `res.json` honoring the `json replacer` setting
- [x] `res.json` honoring the `json spaces` setting
- [x] `res.json` honoring the `json escape` setting
- [x] Automatic `Content-Length` and `Content-Type` charset
- [x] `ETag` (see 4.5)
- [x] Guard against double sends (second `send`/`json` should error or be ignored)
- [x] `res.status().send()` chaining should keep `sent` correct when `status()` is called after `send()`.

### 4.4 Content negotiation (S)

- [x] Implement Accept parsing with q-values for media types.
- [x] Wire `req.accepts()`.
- [x] Wire `req.accepts()`, `req.acceptsCharsets()`, `req.acceptsEncodings()`, and `req.acceptsLanguages()`.
- [ ] Wire `res.format` with 406 when nothing matches.

### 4.5 Caching headers (M)

- [x] Weak and strong ETag generation (setting: `etag`)
- [x] `If-None-Match` handling, producing 304 responses
- [x] Strong ETag generation (`app.set("etag", "strong")`)
- [x] `If-Modified-Since` handling when `If-None-Match` is absent
- [ ] `req.fresh` and `req.stale`
- [x] `Last-Modified` for `sendFile` and `download`

## 5. Phase 3: Features people expect from Express apps

### 5.1 Cookies (M)

- [x] Parse the `Cookie` header into `req.cookies`.
- [x] `res.cookie` options: `maxAge`, `expires`, `path`, `domain`, `secure`, `httpOnly`, `sameSite`.
- [ ] Signed cookies with HMAC and `req.signedCookies` (needs a secret, so `cookieParser(secret)`).
- [x] Multiple `Set-Cookie` headers.

### 5.2 Static files and file sending (M-L)

- [x] Path traversal protection for rooted `sendFile`/`download` (resolve symlinks and verify the target remains under root).
- [x] MIME type lookup by extension for file responses.

- [ ] `express.static(root, opts)` with `index`, `dotfiles`, `extensions`, `maxAge`, `immutable`, `redirect`, `fallthrough`, `setHeaders`.
- [ ] Path traversal protection (reject `..` and encoded variants, confirm the resolved path stays under root).
- [ ] Range requests (`206`, `Content-Range`) and `If-Range`.
- [x] Buffered `res.sendFile` and `res.download` with `Content-Disposition`; Express error callbacks remain pending.
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

- [x] `app.set(name, value)`, `app.get(name)`, `app.enable/disable/enabled/disabled`
- [ ] Settings to honor: `json replacer`, `trust proxy`, `subdomain offset`, `views`, `view engine`, `view cache` (case-sensitive routing, strict routing, environment, weak/strong ETag, x-powered-by, simple/extended query-parser, json spaces, json escape, and JSONP callback-name settings are implemented)
- [ ] Inheritance: a mounted child app inherits parent settings unless overridden.
- [x] `app.get(path, ...handlers)` vs `app.get(setting)` dispatch by arity (Express behavior).

### 6.2 `app.locals` and `res.locals` (S)

- [x] `app.locals` available to all handlers.
- [x] `res.locals` is per request and available to handlers.

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
