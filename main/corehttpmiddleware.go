package main

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// ---------------------------------------------------------------------------
// Native middleware infrastructure
//
// Native middleware are objects (not functions) that app.use()/app.get() accept as handlers.
// They run synchronously on the VM thread and may register finish hooks that run just before
// the response is written (compression, logging, session persistence, response time).
// ---------------------------------------------------------------------------

type httpMiddleware struct {
	name string
	run  func(d *httpDispatch, match httpMatch) httpMiddlewareResult
}

type httpMiddlewareResult struct {
	next   bool
	err    OSValue
	hasErr bool
}

func httpNewMiddleware(name string, run func(d *httpDispatch, match httpMatch) httpMiddlewareResult) OSValue {
	object := &Object{Native: &httpMiddleware{name: name, run: run}, Values: make(map[string]OSValue, 1)}
	return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(object)}
}

func httpMiddlewareFrom(value OSValue) *httpMiddleware {
	if value.Type == OSVObject && value.Opaque != nil {
		if middleware, ok := (*Object)(value.Opaque).Native.(*httpMiddleware); ok {
			return middleware
		}
	}
	return nil
}

func httpPass() httpMiddlewareResult { return httpMiddlewareResult{next: true} }

func httpFail(status int, message string) httpMiddlewareResult {
	return httpMiddlewareResult{err: httpErrorValue(status, message), hasErr: true}
}

func httpNativeSend(d *httpDispatch, status int, contentType, body string) {
	d.data.status = status
	d.data.body = body
	if contentType != "" && d.data.headers.Get("Content-Type") == "" {
		d.data.headers.Set("Content-Type", contentType)
	}
	httpMarkResponseSent(d.data)
}

func httpMiddlewareFactories() map[string]func(vm *VM, args []OSValue) OSValue {
	return map[string]func(vm *VM, args []OSValue) OSValue{
		"json":           httpJSONParserFactory,
		"urlencoded":     httpURLEncodedFactory,
		"text":           httpTextParserFactory,
		"raw":            httpRawParserFactory,
		"multipart":      httpMultipartFactory,
		"static":         httpStaticFactory,
		"cookieParser":   httpCookieParserFactory,
		"session":        httpSessionFactory,
		"cors":           httpCORSFactory,
		"compression":    httpCompressionFactory,
		"logger":         httpLoggerFactory,
		"rateLimit":      httpRateLimitFactory,
		"helmet":         httpHelmetFactory,
		"methodOverride": httpMethodOverrideFactory,
		"responseTime":   httpResponseTimeFactory,
	}
}

// ---------------------------------------------------------------------------
// Option helpers
// ---------------------------------------------------------------------------

func httpOptions(args []OSValue, index int) *Object {
	if len(args) > index && args[index].Type == OSVObject && args[index].Opaque != nil {
		return (*Object)(args[index].Opaque)
	}
	return nil
}

func httpOpt(options *Object, key string) (OSValue, bool) {
	if options == nil {
		return OSValue{}, false
	}
	value, ok := options.Values[key]
	return value, ok
}

func httpOptBool(options *Object, key string, fallback bool) bool {
	if value, ok := httpOpt(options, key); ok {
		return CoerceToBool(value)
	}
	return fallback
}

func httpOptNumber(options *Object, key string, fallback float64) float64 {
	if value, ok := httpOpt(options, key); ok && IsNumber(value) {
		return value.Number
	}
	return fallback
}

func httpOptString(options *Object, key, fallback string) string {
	if value, ok := httpOpt(options, key); ok && IsString(value) {
		return value.StringValue()
	}
	return fallback
}

func httpOptStrings(options *Object, key string) []string {
	if value, ok := httpOpt(options, key); ok {
		return httpCollectStrings([]OSValue{value})
	}
	return nil
}

// httpParseSize accepts a byte count or strings such as "100kb", "1mb", "2gb".
func httpParseSize(value OSValue) int64 {
	if IsNumber(value) {
		return int64(value.Number)
	}
	if !IsString(value) {
		return 0
	}
	text := strings.ToLower(strings.TrimSpace(value.StringValue()))
	units := []struct {
		suffix string
		factor float64
	}{{"tb", 1 << 40}, {"gb", 1 << 30}, {"mb", 1 << 20}, {"kb", 1 << 10}, {"b", 1}}
	for _, unit := range units {
		if strings.HasSuffix(text, unit.suffix) {
			if number, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(text, unit.suffix)), 64); err == nil {
				return int64(number * unit.factor)
			}
			return 0
		}
	}
	number, _ := strconv.ParseFloat(text, 64)
	return int64(number)
}

func httpOptSize(options *Object, key string, fallback int64) int64 {
	if value, ok := httpOpt(options, key); ok {
		if parsed := httpParseSize(value); parsed > 0 {
			return parsed
		}
	}
	return fallback
}

// ---------------------------------------------------------------------------
// Content-type matching and cookie signing
// ---------------------------------------------------------------------------

// httpTypeIs matches a Content-Type against patterns: "application/json", "text/*", "+json", "json", "*/*".
func httpTypeIs(contentType string, patterns []string) bool {
	if strings.TrimSpace(contentType) == "" {
		return false
	}
	actual, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	actual = strings.ToLower(actual)
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		switch {
		case pattern == "":
			continue
		case pattern == "*/*" || pattern == "*":
			return true
		case strings.HasPrefix(pattern, "+"):
			if strings.HasSuffix(actual, pattern) {
				return true
			}
		default:
			if !strings.Contains(pattern, "/") {
				pattern = strings.ToLower(httpMimeFor(pattern))
				if pattern == "" {
					continue
				}
			}
			wanted, have := strings.SplitN(pattern, "/", 2), strings.SplitN(actual, "/", 2)
			if len(wanted) != 2 || len(have) != 2 {
				continue
			}
			if (wanted[0] == "*" || wanted[0] == have[0]) && (wanted[1] == "*" || wanted[1] == have[1]) {
				return true
			}
		}
	}
	return false
}

func httpCookieMAC(value, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(value))
	return strings.TrimRight(base64.StdEncoding.EncodeToString(mac.Sum(nil)), "=")
}

func httpSignCookie(value, secret string) string {
	return "s:" + value + "." + httpCookieMAC(value, secret)
}

func httpUnsignCookie(value string, secrets []string) (string, bool) {
	if !strings.HasPrefix(value, "s:") {
		return "", false
	}
	body := value[2:]
	dot := strings.LastIndexByte(body, '.')
	if dot < 0 {
		return "", false
	}
	payload, signature := body[:dot], body[dot+1:]
	for _, secret := range secrets {
		if hmac.Equal([]byte(signature), []byte(httpCookieMAC(payload, secret))) {
			return payload, true
		}
	}
	return "", false
}

func httpCookieValue(vm *VM, value string) OSValue {
	if strings.HasPrefix(value, "j:") {
		vm.NativeError = ""
		parsed := nativeJSONParse(vm, []OSValue{coreHTTPString(value[2:])})
		if vm.NativeError == "" {
			return parsed
		}
		vm.NativeError = ""
	}
	return coreHTTPString(value)
}

// cookieParser([secret | [secrets]]): signed cookies ("s:" prefix) go to req.signedCookies, "j:" values are JSON.
func httpCookieParserFactory(vm *VM, args []OSValue) OSValue {
	var secrets []string
	if len(args) > 0 {
		secrets = httpCollectStrings(args[:1])
	}
	return httpNewMiddleware("cookieParser", func(d *httpDispatch, _ httpMatch) httpMiddlewareResult {
		d.reqData.secrets = secrets
		plain := make(map[string]OSValue)
		signed := make(map[string]OSValue)
		for _, cookie := range (&http.Request{Header: d.reqData.headers}).Cookies() {
			value := cookie.Value
			if decoded, err := url.PathUnescape(value); err == nil {
				value = decoded
			}
			if len(secrets) > 0 && strings.HasPrefix(value, "s:") {
				if payload, ok := httpUnsignCookie(value, secrets); ok {
					signed[cookie.Name] = httpCookieValue(d.vm, payload)
				} else {
					signed[cookie.Name] = httpBool(false)
				}
				continue
			}
			plain[cookie.Name] = httpCookieValue(d.vm, value)
		}
		request := (*Object)(d.request.Opaque)
		request.Set("cookies", httpValueObject(plain))
		request.Set("signedCookies", httpValueObject(signed))
		if len(secrets) > 0 {
			request.Set("secret", coreHTTPString(secrets[0]))
		}
		return httpPass()
	})
}

// ---------------------------------------------------------------------------
// Body parsers
// ---------------------------------------------------------------------------

type httpBodyError struct {
	status  int
	message string
}

func httpHasBody(data *httpRequestData) bool {
	return len(data.body) > 0 || data.headers.Get("Transfer-Encoding") != ""
}

// httpDecodeBody applies Content-Encoding (gzip/deflate), the size limit and the charset.
func httpDecodeBody(data *httpRequestData, limit int64, inflate bool) (string, *httpBodyError) {
	raw := data.body
	encoding := strings.ToLower(strings.TrimSpace(data.headers.Get("Content-Encoding")))
	if encoding != "" && encoding != "identity" {
		if !inflate {
			return "", &httpBodyError{415, "content encoding unsupported"}
		}
		var reader io.Reader
		switch encoding {
		case "gzip", "x-gzip":
			gz, err := gzip.NewReader(strings.NewReader(raw))
			if err != nil {
				return "", &httpBodyError{400, "invalid gzip body"}
			}
			reader = gz
		case "deflate":
			if z, err := zlib.NewReader(strings.NewReader(raw)); err == nil {
				reader = z
			} else {
				reader = flate.NewReader(strings.NewReader(raw))
			}
		default:
			return "", &httpBodyError{415, "unsupported content encoding \"" + encoding + "\""}
		}
		decoded, err := io.ReadAll(io.LimitReader(reader, limit+1))
		if err != nil {
			return "", &httpBodyError{400, "invalid compressed body"}
		}
		raw = string(decoded)
	}
	if int64(len(raw)) > limit {
		return "", &httpBodyError{413, "request entity too large"}
	}
	_, params, _ := mime.ParseMediaType(data.headers.Get("Content-Type"))
	switch charset := strings.ToLower(params["charset"]); charset {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return raw, nil
	case "iso-8859-1", "iso8859-1", "latin1", "windows-1252":
		var builder strings.Builder
		for index := 0; index < len(raw); index++ {
			builder.WriteRune(rune(raw[index]))
		}
		return builder.String(), nil
	default:
		return "", &httpBodyError{415, "unsupported charset \"" + strings.ToUpper(charset) + "\""}
	}
}

func httpBodyParser(name string, defaultTypes []string, options *Object, defaultLimit int64,
	parse func(vm *VM, text string) (OSValue, *httpBodyError)) OSValue {
	limit := httpOptSize(options, "limit", defaultLimit)
	inflate := httpOptBool(options, "inflate", true)
	types := httpOptStrings(options, "type")
	if len(types) == 0 {
		types = defaultTypes
	}
	return httpNewMiddleware(name, func(d *httpDispatch, _ httpMatch) httpMiddlewareResult {
		data := d.reqData
		if data.bodyParsed || !httpHasBody(data) || !httpTypeIs(data.headers.Get("Content-Type"), types) {
			return httpPass()
		}
		text, failure := httpDecodeBody(data, limit, inflate)
		if failure == nil {
			var value OSValue
			if value, failure = parse(d.vm, text); failure == nil {
				data.bodyParsed = true
				(*Object)(d.request.Opaque).Set("body", value)
				return httpPass()
			}
		}
		return httpFail(failure.status, failure.message)
	})
}

func httpJSONParserFactory(_ *VM, args []OSValue) OSValue {
	options := httpOptions(args, 0)
	strict := httpOptBool(options, "strict", true)
	return httpBodyParser("json", []string{"application/json"}, options, 100<<10, func(vm *VM, text string) (OSValue, *httpBodyError) {
		trimmed := strings.TrimSpace(strings.TrimPrefix(text, "\ufeff"))
		if trimmed == "" {
			return httpValueObject(nil), nil
		}
		if strict && trimmed[0] != '{' && trimmed[0] != '[' {
			return OSValue{}, &httpBodyError{400, "invalid JSON, only objects and arrays are accepted"}
		}
		vm.NativeError = ""
		value := nativeJSONParse(vm, []OSValue{coreHTTPString(trimmed)})
		if vm.NativeError != "" {
			vm.NativeError = ""
			return OSValue{}, &httpBodyError{400, "invalid JSON body"}
		}
		return value, nil
	})
}

func httpURLEncodedFactory(_ *VM, args []OSValue) OSValue {
	options := httpOptions(args, 0)
	parser := "simple"
	if httpOptBool(options, "extended", false) {
		parser = "extended"
	}
	parameterLimit := int(httpOptNumber(options, "parameterLimit", 1000))
	return httpBodyParser("urlencoded", []string{"application/x-www-form-urlencoded"}, options, 100<<10, func(_ *VM, text string) (OSValue, *httpBodyError) {
		if text != "" && strings.Count(text, "&")+1 > parameterLimit {
			return OSValue{}, &httpBodyError{413, "too many parameters"}
		}
		return httpValueObject(httpParseQuery(text, parser)), nil
	})
}

func httpTextParserFactory(_ *VM, args []OSValue) OSValue {
	return httpBodyParser("text", []string{"text/plain"}, httpOptions(args, 0), 100<<10, func(_ *VM, text string) (OSValue, *httpBodyError) {
		return coreHTTPString(text), nil
	})
}

// raw() exposes the body as a string of bytes: the VM has no Buffer type.
func httpRawParserFactory(_ *VM, args []OSValue) OSValue {
	return httpBodyParser("raw", []string{"application/octet-stream"}, httpOptions(args, 0), 100<<10, func(_ *VM, text string) (OSValue, *httpBodyError) {
		return coreHTTPString(text), nil
	})
}

// multipart({ storage: "memory"|"disk", dest, maxFiles, maxFields, fileSize }) fills req.body, req.files and req.file.
func httpMultipartFactory(_ *VM, args []OSValue) OSValue {
	options := httpOptions(args, 0)
	storage := httpOptString(options, "storage", "memory")
	dest := httpOptString(options, "dest", os.TempDir())
	maxFiles := int(httpOptNumber(options, "maxFiles", 10))
	maxFields := int(httpOptNumber(options, "maxFields", 100))
	fileLimit := httpOptSize(options, "fileSize", 5<<20)
	return httpNewMiddleware("multipart", func(d *httpDispatch, _ httpMatch) httpMiddlewareResult {
		data := d.reqData
		contentType := data.headers.Get("Content-Type")
		if data.bodyParsed || !httpTypeIs(contentType, []string{"multipart/form-data"}) {
			return httpPass()
		}
		_, params, err := mime.ParseMediaType(contentType)
		if err != nil || params["boundary"] == "" {
			return httpFail(400, "missing multipart boundary")
		}
		reader := multipart.NewReader(strings.NewReader(data.body), params["boundary"])
		fields := make(map[string]any)
		var files []OSValue
		fieldCount := 0
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return httpFail(400, "malformed multipart body")
			}
			name := part.FormName()
			if part.FileName() == "" {
				fieldCount++
				if fieldCount > maxFields {
					return httpFail(413, "too many fields")
				}
				value, _ := io.ReadAll(io.LimitReader(part, 1<<20))
				httpQueryInsert(fields, httpQueryPath(name), string(value))
				continue
			}
			if len(files)+1 > maxFiles {
				return httpFail(413, "too many files")
			}
			content, _ := io.ReadAll(io.LimitReader(part, fileLimit+1))
			if int64(len(content)) > fileLimit {
				return httpFail(413, "file too large")
			}
			info := map[string]OSValue{
				"fieldname":    coreHTTPString(name),
				"originalname": coreHTTPString(filepath.Base(part.FileName())),
				"mimetype":     coreHTTPString(part.Header.Get("Content-Type")),
				"size":         httpNum(float64(len(content))),
			}
			if storage == "disk" {
				file, err := os.CreateTemp(dest, "upload-*")
				if err != nil {
					return httpFail(500, "cannot store upload")
				}
				_, writeErr := file.Write(content)
				closeErr := file.Close()
				if writeErr != nil || closeErr != nil {
					return httpFail(500, "cannot store upload")
				}
				info["path"] = coreHTTPString(file.Name())
			} else {
				info["content"] = coreHTTPString(string(content))
			}
			files = append(files, httpValueObject(info))
		}
		data.bodyParsed = true
		request := (*Object)(d.request.Opaque)
		request.Set("body", httpQueryValue(fields))
		request.Set("files", httpArray(files))
		if len(files) > 0 {
			request.Set("file", files[0])
		}
		return httpPass()
	})
}

// ---------------------------------------------------------------------------
// express.static
// ---------------------------------------------------------------------------

// static(root, { index, dotfiles, extensions, maxAge, immutable, redirect, fallthrough, setHeaders, etag, lastModified })
// Files are streamed with Range and If-Range support via http.ServeContent.
func httpStaticFactory(vm *VM, args []OSValue) OSValue {
	if len(args) == 0 || !IsString(args[0]) {
		vm.NativeError = "core:http static expects a root directory"
		return vm.Null
	}
	options := httpOptions(args, 1)
	rootAbs, err := filepath.Abs(args[0].StringValue())
	if err != nil {
		vm.NativeError = "core:http static: " + err.Error()
		return vm.Null
	}
	index := []string{"index.html"}
	if value, ok := httpOpt(options, "index"); ok {
		if value.Type == OSVBool && !CoerceToBool(value) {
			index = nil
		} else {
			index = httpCollectStrings([]OSValue{value})
		}
	}
	dotfiles := httpOptString(options, "dotfiles", "ignore")
	var extensions []string
	for _, ext := range httpOptStrings(options, "extensions") {
		extensions = append(extensions, strings.TrimPrefix(ext, "."))
	}
	redirect := httpOptBool(options, "redirect", true)
	fallThrough := httpOptBool(options, "fallthrough", true)
	var setHeaders OSValue
	if value, ok := httpOpt(options, "setHeaders"); ok && value.Type == OSVFunction {
		setHeaders = value
	}
	fileOptions := httpFileOptionsFrom(options, true)
	return httpNewMiddleware("static", func(d *httpDispatch, match httpMatch) httpMiddlewareResult {
		miss := func(status int, message string) httpMiddlewareResult {
			if fallThrough && status == http.StatusNotFound {
				return httpPass()
			}
			return httpFail(status, message)
		}
		if d.method != http.MethodGet && d.method != http.MethodHead {
			if fallThrough {
				return httpPass()
			}
			d.data.headers.Set("Allow", "GET, HEAD")
			return httpFail(http.StatusMethodNotAllowed, "Method Not Allowed")
		}
		requestPath := match.url
		if strings.ContainsRune(requestPath, 0) {
			return httpFail(http.StatusBadRequest, "Bad Request")
		}
		for _, segment := range strings.Split(requestPath, "/") {
			if segment == ".." {
				return httpFail(http.StatusForbidden, "Forbidden")
			}
		}
		if httpDotfileBlocked(requestPath, dotfiles) {
			if dotfiles == "deny" {
				return httpFail(http.StatusForbidden, "Forbidden")
			}
			return miss(http.StatusNotFound, "Not Found")
		}
		target := filepath.Join(rootAbs, filepath.FromSlash(strings.TrimPrefix(requestPath, "/")))
		info, err := os.Stat(target)
		if err != nil && !strings.HasSuffix(requestPath, "/") {
			for _, ext := range extensions {
				if candidate, statErr := os.Stat(target + "." + ext); statErr == nil && candidate.Mode().IsRegular() {
					info, target, err = candidate, target+"."+ext, nil
					break
				}
			}
		}
		if err != nil {
			return miss(http.StatusNotFound, "Not Found")
		}
		if info.IsDir() {
			if !strings.HasSuffix(d.incoming.path, "/") {
				if !redirect {
					return miss(http.StatusNotFound, "Not Found")
				}
				location := d.incoming.path + "/" + d.queryPart
				d.data.headers.Set("Location", location)
				httpNativeSend(d, http.StatusMovedPermanently, "text/plain; charset=utf-8", "Redirecting to "+location)
				return httpMiddlewareResult{}
			}
			found := false
			for _, name := range index {
				candidate := filepath.Join(target, name)
				if candidateInfo, statErr := os.Stat(candidate); statErr == nil && candidateInfo.Mode().IsRegular() {
					target, info, found = candidate, candidateInfo, true
					break
				}
			}
			if !found {
				return miss(http.StatusNotFound, "Not Found")
			}
		}
		real, err := filepath.EvalSymlinks(target)
		rootReal, rootErr := filepath.EvalSymlinks(rootAbs)
		if err != nil || rootErr != nil {
			return miss(http.StatusNotFound, "Not Found")
		}
		if relative, relErr := filepath.Rel(rootReal, real); relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return httpFail(http.StatusForbidden, "Forbidden")
		}
		if setHeaders.Type == OSVFunction {
			stat := httpValueObject(map[string]OSValue{
				"size":  httpNum(float64(info.Size())),
				"mtime": httpNum(float64(info.ModTime().UnixMilli())),
			})
			runHTTPHandler(d.vm, setHeaders, d.response, coreHTTPString(real), stat)
		}
		perRequest := fileOptions
		perRequest.etag = perRequest.etag && d.incoming.etagEnabled
		httpAttachFile(d.data, info, real, perRequest)
		return httpMiddlewareResult{}
	})
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

type httpSessionStore interface {
	Get(vm *VM, id string) (string, bool)
	Set(vm *VM, id, data string, ttl time.Duration)
	Destroy(vm *VM, id string)
}

type httpSessionEntry struct {
	data    string
	expires time.Time
}

type httpMemorySessions struct {
	mu    sync.Mutex
	items map[string]httpSessionEntry
}

func (store *httpMemorySessions) Get(_ *VM, id string) (string, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	entry, ok := store.items[id]
	if !ok || time.Now().After(entry.expires) {
		delete(store.items, id)
		return "", false
	}
	return entry.data, true
}

func (store *httpMemorySessions) Set(_ *VM, id, data string, ttl time.Duration) {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := time.Now()
	if len(store.items) > 10000 {
		for key, entry := range store.items {
			if now.After(entry.expires) {
				delete(store.items, key)
			}
		}
	}
	store.items[id] = httpSessionEntry{data: data, expires: now.Add(ttl)}
}

func (store *httpMemorySessions) Destroy(_ *VM, id string) {
	store.mu.Lock()
	delete(store.items, id)
	store.mu.Unlock()
}

// httpScriptSessions adapts a script object { get(id), set(id, json, ttlMs), destroy(id) } (synchronous methods).
type httpScriptSessions struct{ object *Object }

func (store *httpScriptSessions) Get(vm *VM, id string) (string, bool) {
	if fn, ok := store.object.Values["get"]; ok && fn.Type == OSVFunction {
		completion := runHTTPHandler(vm, fn, coreHTTPString(id))
		if completion.Resolved && !completion.Rejected && IsString(completion.Value) {
			return completion.Value.StringValue(), true
		}
	}
	return "", false
}

func (store *httpScriptSessions) Set(vm *VM, id, data string, ttl time.Duration) {
	if fn, ok := store.object.Values["set"]; ok && fn.Type == OSVFunction {
		runHTTPHandler(vm, fn, coreHTTPString(id), coreHTTPString(data), httpNum(float64(ttl.Milliseconds())))
	}
}

func (store *httpScriptSessions) Destroy(vm *VM, id string) {
	if fn, ok := store.object.Values["destroy"]; ok && fn.Type == OSVFunction {
		runHTTPHandler(vm, fn, coreHTTPString(id))
	}
}

type httpSessionRuntime struct {
	id        string
	saved     string
	isNew     bool
	destroyed bool
}

var httpSessionReserved = map[string]bool{"id": true, "cookie": true, "save": true, "destroy": true, "regenerate": true}

func httpNewSessionID() string {
	buffer := make([]byte, 24)
	_, _ = rand.Read(buffer)
	return base64.RawURLEncoding.EncodeToString(buffer)
}

func httpSessionSnapshot(vm *VM, session *Object) string {
	data := &Object{Values: make(map[string]OSValue, len(session.Values))}
	for _, key := range session.Keys {
		if !httpSessionReserved[key] {
			if value, ok := session.Values[key]; ok {
				data.Set(key, value)
			}
		}
	}
	vm.NativeError = ""
	encoded := nativeJSONStringify(vm, []OSValue{{Type: OSVObject, Opaque: unsafe.Pointer(data)}})
	if vm.NativeError != "" {
		vm.NativeError = ""
		return "{}"
	}
	return encoded.StringValue()
}

// session({ secret, name, cookie, store, rolling, saveUninitialized }) -> req.session with save/destroy/regenerate.
func httpSessionFactory(vm *VM, args []OSValue) OSValue {
	options := httpOptions(args, 0)
	secrets := httpOptStrings(options, "secret")
	if len(secrets) == 0 {
		vm.NativeError = "core:http session requires a secret"
		return vm.Null
	}
	name := httpOptString(options, "name", "connect.sid")
	saveUninitialized := httpOptBool(options, "saveUninitialized", false)
	rolling := httpOptBool(options, "rolling", false)
	cookieOptions := map[string]OSValue{"httpOnly": httpBool(true), "path": coreHTTPString("/")}
	if value, ok := httpOpt(options, "cookie"); ok && value.Type == OSVObject && value.Opaque != nil {
		for key, option := range (*Object)(value.Opaque).Values {
			cookieOptions[key] = option
		}
	}
	ttl := 24 * time.Hour
	if maxAge, ok := cookieOptions["maxAge"]; ok && IsNumber(maxAge) && maxAge.Number > 0 {
		ttl = time.Duration(maxAge.Number) * time.Millisecond
	}
	var store httpSessionStore = &httpMemorySessions{items: make(map[string]httpSessionEntry)}
	if value, ok := httpOpt(options, "store"); ok && value.Type == OSVObject && value.Opaque != nil {
		store = &httpScriptSessions{object: (*Object)(value.Opaque)}
	}
	return httpNewMiddleware("session", func(d *httpDispatch, _ httpMatch) httpMiddlewareResult {
		request := (*Object)(d.request.Opaque)
		if _, attached := request.Values["session"]; attached {
			return httpPass()
		}
		vm := d.vm
		id := ""
		for _, cookie := range (&http.Request{Header: d.reqData.headers}).Cookies() {
			if cookie.Name == name {
				value := cookie.Value
				if decoded, err := url.PathUnescape(value); err == nil {
					value = decoded
				}
				if payload, ok := httpUnsignCookie(value, secrets); ok {
					id = payload
				}
				break
			}
		}
		stored, loaded := "", false
		if id != "" {
			stored, loaded = store.Get(vm, id)
		}
		runtime := &httpSessionRuntime{id: id, isNew: !loaded}
		if !loaded {
			runtime.id = httpNewSessionID()
			stored = "{}"
		}
		session := &Object{Values: make(map[string]OSValue, 8)}
		sessionValue := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(session)}
		vm.NativeError = ""
		if parsed := nativeJSONParse(vm, []OSValue{coreHTTPString(stored)}); vm.NativeError == "" {
			httpMergeInto(session, parsed)
		}
		vm.NativeError = ""
		runtime.saved = httpSessionSnapshot(vm, session)
		session.Set("id", coreHTTPString(runtime.id))
		session.Set("cookie", httpValueObject(cookieOptions))
		callback := func(vm *VM, callArgs []OSValue) {
			if len(callArgs) > 0 && callArgs[0].Type == OSVFunction {
				runHTTPHandler(vm, callArgs[0], vm.Null)
			}
		}
		session.Set("save", httpBoundMethod(sessionValue, -1, func(_ OSValue, vm *VM, callArgs []OSValue) OSValue {
			runtime.saved = httpSessionSnapshot(vm, session)
			store.Set(vm, runtime.id, runtime.saved, ttl)
			callback(vm, callArgs)
			return vm.Null
		}))
		session.Set("destroy", httpBoundMethod(sessionValue, -1, func(_ OSValue, vm *VM, callArgs []OSValue) OSValue {
			runtime.destroyed = true
			store.Destroy(vm, runtime.id)
			callback(vm, callArgs)
			return vm.Null
		}))
		session.Set("regenerate", httpBoundMethod(sessionValue, -1, func(_ OSValue, vm *VM, callArgs []OSValue) OSValue {
			store.Destroy(vm, runtime.id)
			runtime.id, runtime.isNew, runtime.saved = httpNewSessionID(), true, ""
			session.Set("id", coreHTTPString(runtime.id))
			callback(vm, callArgs)
			return vm.Null
		}))
		request.Set("session", sessionValue)
		request.Set("sessionID", coreHTTPString(runtime.id))
		d.data.finishHooks = append(d.data.finishHooks, func(d *httpDispatch) {
			if runtime.destroyed {
				expired := map[string]OSValue{"path": coreHTTPString("/"), "expires": httpNum(0), "maxAge": httpNum(-1)}
				d.data.headers.Add("Set-Cookie", httpBuildCookie(name, "", expired).String())
				return
			}
			snapshot := httpSessionSnapshot(d.vm, session)
			if runtime.isNew && !saveUninitialized && snapshot == "{}" {
				return
			}
			if snapshot != runtime.saved || runtime.isNew {
				store.Set(d.vm, runtime.id, snapshot, ttl)
				runtime.saved = snapshot
			}
			if runtime.isNew || rolling {
				cookie := httpBuildCookie(name, httpSignCookie(runtime.id, secrets[0]), cookieOptions)
				d.data.headers.Add("Set-Cookie", cookie.String())
				runtime.isNew = false
			}
		})
		return httpPass()
	})
}

// ---------------------------------------------------------------------------
// CORS
// ---------------------------------------------------------------------------

// cors({ origin, methods, allowedHeaders, exposedHeaders, credentials, maxAge, preflightContinue, optionsSuccessStatus })
func httpCORSFactory(_ *VM, args []OSValue) OSValue {
	options := httpOptions(args, 0)
	origin, hasOrigin := httpOpt(options, "origin")
	methods := "GET,HEAD,PUT,PATCH,POST,DELETE"
	if list := httpOptStrings(options, "methods"); len(list) > 0 {
		methods = strings.Join(list, ",")
	}
	allowedHeaders := httpOptStrings(options, "allowedHeaders")
	exposedHeaders := httpOptStrings(options, "exposedHeaders")
	credentials := httpOptBool(options, "credentials", false)
	maxAge, hasMaxAge := httpOpt(options, "maxAge")
	preflightContinue := httpOptBool(options, "preflightContinue", false)
	successStatus := int(httpOptNumber(options, "optionsSuccessStatus", 204))
	return httpNewMiddleware("cors", func(d *httpDispatch, _ httpMatch) httpMiddlewareResult {
		headers := d.data.headers
		requestOrigin := d.reqData.headers.Get("Origin")
		allow, vary := "", false
		switch {
		case !hasOrigin:
			allow = "*"
		case origin.Type == OSVBool:
			if CoerceToBool(origin) {
				allow, vary = requestOrigin, true
			}
		case IsString(origin):
			allow, vary = origin.StringValue(), origin.StringValue() != "*"
		case origin.Type == OSVArray:
			vary = true
			if httpContains(httpCollectStrings([]OSValue{origin}), requestOrigin) {
				allow = requestOrigin
			}
		}
		if vary {
			httpVary(d.data, "Origin")
		}
		if allow != "" {
			headers.Set("Access-Control-Allow-Origin", allow)
		}
		if credentials {
			headers.Set("Access-Control-Allow-Credentials", "true")
		}
		if len(exposedHeaders) > 0 {
			headers.Set("Access-Control-Expose-Headers", strings.Join(exposedHeaders, ","))
		}
		if d.method != http.MethodOptions {
			return httpPass()
		}
		headers.Set("Access-Control-Allow-Methods", methods)
		if len(allowedHeaders) > 0 {
			headers.Set("Access-Control-Allow-Headers", strings.Join(allowedHeaders, ","))
		} else if requested := d.reqData.headers.Get("Access-Control-Request-Headers"); requested != "" {
			headers.Set("Access-Control-Allow-Headers", requested)
			httpVary(d.data, "Access-Control-Request-Headers")
		}
		if hasMaxAge {
			headers.Set("Access-Control-Max-Age", maxAge.String())
		}
		if preflightContinue {
			return httpPass()
		}
		d.data.status = successStatus
		headers.Set("Content-Length", "0")
		httpMarkResponseSent(d.data)
		return httpMiddlewareResult{}
	})
}

// ---------------------------------------------------------------------------
// Compression (gzip and deflate; brotli is not in the Go standard library)
// ---------------------------------------------------------------------------

func httpCompressible(contentType string) bool {
	base, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	base = strings.ToLower(base)
	return strings.HasPrefix(base, "text/") || base == "application/json" || base == "application/javascript" ||
		base == "application/xml" || base == "image/svg+xml" || strings.HasSuffix(base, "+json") || strings.HasSuffix(base, "+xml")
}

func httpCompressionFactory(_ *VM, args []OSValue) OSValue {
	options := httpOptions(args, 0)
	threshold := int(httpOptSize(options, "threshold", 1024))
	level := int(httpOptNumber(options, "level", -1))
	return httpNewMiddleware("compression", func(d *httpDispatch, _ httpMatch) httpMiddlewareResult {
		d.data.finishHooks = append(d.data.finishHooks, func(d *httpDispatch) {
			data := d.data
			if data.file != nil || data.streaming || d.method == http.MethodHead || data.status < 200 ||
				data.status == http.StatusNoContent || data.status == http.StatusNotModified ||
				data.headers.Get("Content-Encoding") != "" ||
				strings.Contains(strings.ToLower(data.headers.Get("Cache-Control")), "no-transform") {
				return
			}
			httpVary(data, "Accept-Encoding")
			acceptEncoding := d.reqData.headers.Get("Accept-Encoding")
			if acceptEncoding == "" || len(data.body) < threshold || !httpCompressible(data.headers.Get("Content-Type")) {
				return
			}
			encoding := httpBestToken(acceptEncoding, "Accept-Encoding", []string{"gzip", "deflate"})
			var buffer bytes.Buffer
			switch encoding {
			case "gzip":
				writer, err := gzip.NewWriterLevel(&buffer, level)
				if err != nil {
					writer = gzip.NewWriter(&buffer)
				}
				_, _ = writer.Write([]byte(data.body))
				_ = writer.Close()
			case "deflate":
				writer, err := zlib.NewWriterLevel(&buffer, level)
				if err != nil {
					writer = zlib.NewWriter(&buffer)
				}
				_, _ = writer.Write([]byte(data.body))
				_ = writer.Close()
			default:
				return
			}
			data.body = buffer.String()
			data.headers.Set("Content-Encoding", encoding)
			data.headers.Del("Content-Length")
			if etag := data.headers.Get("ETag"); etag != "" && !strings.HasPrefix(etag, "W/") {
				data.headers.Set("ETag", "W/"+etag)
			}
		})
		return httpPass()
	})
}

// ---------------------------------------------------------------------------
// Logger (morgan-style)
// ---------------------------------------------------------------------------

var httpLogTokenPattern = regexp.MustCompile(`:([a-z-]+)(?:\[([^\]]+)\])?`)

var httpLogFormats = map[string]string{
	"tiny":     ":method :url :status :res[content-length] - :response-time ms",
	"short":    ":remote-addr :remote-user :method :url HTTP/:http-version :status :res[content-length] - :response-time ms",
	"common":   `:remote-addr - :remote-user [:date] ":method :url HTTP/:http-version" :status :res[content-length]`,
	"combined": `:remote-addr - :remote-user [:date] ":method :url HTTP/:http-version" :status :res[content-length] ":referrer" ":user-agent"`,
	"dev":      ":method :url :status :response-time ms - :res[content-length]",
}

func httpLogLine(format string, d *httpDispatch) string {
	data := d.data
	length := data.headers.Get("Content-Length")
	if length == "" {
		switch {
		case data.file != nil:
			length = strconv.FormatInt(data.file.info.Size(), 10)
		case len(data.body) > 0 && data.status != http.StatusNoContent && data.status != http.StatusNotModified:
			length = strconv.Itoa(len(data.body))
		}
	}
	if length == "" {
		length = "-"
	}
	ip := ""
	if value, ok := (*Object)(d.request.Opaque).Values["ip"]; ok {
		ip = value.String()
	}
	return httpLogTokenPattern.ReplaceAllStringFunc(format, func(token string) string {
		parts := httpLogTokenPattern.FindStringSubmatch(token)
		switch parts[1] {
		case "method":
			return d.method
		case "url":
			return d.incoming.path + d.queryPart
		case "status":
			return strconv.Itoa(data.status)
		case "response-time":
			return strconv.FormatFloat(float64(time.Since(d.start))/float64(time.Millisecond), 'f', 3, 64)
		case "remote-addr":
			return ip
		case "remote-user":
			return "-"
		case "date":
			return time.Now().UTC().Format("02/Jan/2006:15:04:05 -0700")
		case "http-version":
			return "1.1"
		case "referrer":
			return d.reqData.headers.Get("Referer")
		case "user-agent":
			return d.reqData.headers.Get("User-Agent")
		case "req":
			if value := d.reqData.headers.Get(parts[2]); value != "" {
				return value
			}
			return "-"
		case "res":
			if strings.EqualFold(parts[2], "content-length") {
				return length
			}
			if value := data.headers.Get(parts[2]); value != "" {
				return value
			}
			return "-"
		}
		return token
	})
}

// logger([format], { write(line) }) writes to stderr unless a write function is supplied.
func httpLoggerFactory(_ *VM, args []OSValue) OSValue {
	format := httpLogFormats["dev"]
	var options *Object
	for index, arg := range args {
		if index == 0 && IsString(arg) {
			if preset, ok := httpLogFormats[arg.StringValue()]; ok {
				format = preset
			} else {
				format = arg.StringValue()
			}
		} else if arg.Type == OSVObject && arg.Opaque != nil {
			options = (*Object)(arg.Opaque)
		}
	}
	if value, ok := httpOpt(options, "format"); ok && IsString(value) {
		if preset, ok := httpLogFormats[value.StringValue()]; ok {
			format = preset
		} else {
			format = value.StringValue()
		}
	}
	var writer OSValue
	if value, ok := httpOpt(options, "write"); ok && value.Type == OSVFunction {
		writer = value
	}
	return httpNewMiddleware("logger", func(d *httpDispatch, _ httpMatch) httpMiddlewareResult {
		d.data.finishHooks = append(d.data.finishHooks, func(d *httpDispatch) {
			line := httpLogLine(format, d)
			if writer.Type == OSVFunction {
				runHTTPHandler(d.vm, writer, coreHTTPString(line))
			} else {
				fmt.Fprintln(os.Stderr, line)
			}
		})
		return httpPass()
	})
}

// ---------------------------------------------------------------------------
// Rate limiting
// ---------------------------------------------------------------------------

type httpRateEntry struct {
	count int
	reset time.Time
}

// rateLimit({ windowMs, limit|max, message, statusCode, standardHeaders }) keyed by req.ip, in memory.
func httpRateLimitFactory(_ *VM, args []OSValue) OSValue {
	options := httpOptions(args, 0)
	window := time.Duration(httpOptNumber(options, "windowMs", 60000) * float64(time.Millisecond))
	limit := int(httpOptNumber(options, "limit", httpOptNumber(options, "max", 60)))
	message := httpOptString(options, "message", "Too Many Requests")
	status := int(httpOptNumber(options, "statusCode", 429))
	standard := httpOptBool(options, "standardHeaders", true)
	var mu sync.Mutex
	entries := make(map[string]httpRateEntry)
	return httpNewMiddleware("rateLimit", func(d *httpDispatch, _ httpMatch) httpMiddlewareResult {
		key := ""
		if value, ok := (*Object)(d.request.Opaque).Values["ip"]; ok {
			key = value.String()
		}
		now := time.Now()
		mu.Lock()
		if len(entries) > 10000 {
			for k, e := range entries {
				if now.After(e.reset) {
					delete(entries, k)
				}
			}
		}
		entry := entries[key]
		if now.After(entry.reset) {
			entry = httpRateEntry{reset: now.Add(window)}
		}
		entry.count++
		entries[key] = entry
		mu.Unlock()
		if standard {
			d.data.headers.Set("RateLimit-Limit", strconv.Itoa(limit))
			d.data.headers.Set("RateLimit-Remaining", strconv.Itoa(max(limit-entry.count, 0)))
			d.data.headers.Set("RateLimit-Reset", strconv.Itoa(int(time.Until(entry.reset).Seconds())+1))
		}
		if entry.count > limit {
			d.data.headers.Set("Retry-After", strconv.Itoa(int(time.Until(entry.reset).Seconds())+1))
			httpNativeSend(d, status, "text/plain; charset=utf-8", message)
			return httpMiddlewareResult{}
		}
		return httpPass()
	})
}

// ---------------------------------------------------------------------------
// Security headers (helmet-style), method override, response time
// ---------------------------------------------------------------------------

var httpHelmetDefaults = []struct{ name, value string }{
	{"Content-Security-Policy", "default-src 'self';base-uri 'self';font-src 'self' https: data:;form-action 'self';frame-ancestors 'self';img-src 'self' data:;object-src 'none';script-src 'self';script-src-attr 'none';style-src 'self' https: 'unsafe-inline';upgrade-insecure-requests"},
	{"Cross-Origin-Opener-Policy", "same-origin"},
	{"Cross-Origin-Resource-Policy", "same-origin"},
	{"Origin-Agent-Cluster", "?1"},
	{"Referrer-Policy", "no-referrer"},
	{"Strict-Transport-Security", "max-age=15552000; includeSubDomains"},
	{"X-Content-Type-Options", "nosniff"},
	{"X-DNS-Prefetch-Control", "off"},
	{"X-Download-Options", "noopen"},
	{"X-Frame-Options", "SAMEORIGIN"},
	{"X-Permitted-Cross-Domain-Policies", "none"},
	{"X-XSS-Protection", "0"},
}

// helmet({ "<Header-Name>": false | "custom value", hidePoweredBy })
func httpHelmetFactory(_ *VM, args []OSValue) OSValue {
	options := httpOptions(args, 0)
	hide := httpOptBool(options, "hidePoweredBy", true)
	return httpNewMiddleware("helmet", func(d *httpDispatch, _ httpMatch) httpMiddlewareResult {
		for _, header := range httpHelmetDefaults {
			value := header.value
			if override, ok := httpOpt(options, header.name); ok {
				if override.Type == OSVBool && !CoerceToBool(override) {
					continue
				}
				if IsString(override) {
					value = override.StringValue()
				}
			}
			d.data.headers.Set(header.name, value)
		}
		if hide {
			d.incoming.poweredBy = false
			d.data.headers.Del("X-Powered-By")
		}
		return httpPass()
	})
}

var httpOverrideMethods = map[string]bool{"GET": true, "HEAD": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true, "OPTIONS": true}

// methodOverride([header-or-query-key], { methods }) rewrites req.method; later layers see the new method.
func httpMethodOverrideFactory(_ *VM, args []OSValue) OSValue {
	getter := "X-HTTP-Method-Override"
	if len(args) > 0 && IsString(args[0]) {
		getter = args[0].StringValue()
	}
	options := httpOptions(args, len(args)-1)
	methods := []string{"POST"}
	if list := httpOptStrings(options, "methods"); len(list) > 0 {
		methods = list
	}
	fromHeader := strings.HasPrefix(strings.ToLower(getter), "x-")
	return httpNewMiddleware("methodOverride", func(d *httpDispatch, _ httpMatch) httpMiddlewareResult {
		if !httpContains(methods, d.method) {
			return httpPass()
		}
		value := ""
		if fromHeader {
			value = d.reqData.headers.Get(getter)
		} else if query, err := url.ParseQuery(d.incoming.query); err == nil {
			value = query.Get(getter)
		}
		override := strings.ToUpper(strings.TrimSpace(strings.Split(value, ",")[0]))
		if httpOverrideMethods[override] {
			d.method = override
			(*Object)(d.request.Opaque).Set("method", coreHTTPString(override))
		}
		return httpPass()
	})
}

func httpResponseTimeFactory(_ *VM, args []OSValue) OSValue {
	options := httpOptions(args, 0)
	digits := int(httpOptNumber(options, "digits", 3))
	header := httpOptString(options, "header", "X-Response-Time")
	suffix := httpOptBool(options, "suffix", true)
	return httpNewMiddleware("responseTime", func(d *httpDispatch, _ httpMatch) httpMiddlewareResult {
		d.data.finishHooks = append(d.data.finishHooks, func(d *httpDispatch) {
			value := strconv.FormatFloat(float64(time.Since(d.start))/float64(time.Millisecond), 'f', digits, 64)
			if suffix {
				value += "ms"
			}
			d.data.headers.Set(header, value)
		})
		return httpPass()
	})
}
