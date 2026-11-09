package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"mime"
	"net/http"
	"strings"
	"unsafe"
)

type httpMiddlewareResult struct {
	next   bool
	hasErr bool
	err    OSValue
}

type httpMiddleware struct {
	kind      string
	limit     int
	strict    bool
	extended  bool
	typeValue string
	secrets   []string
}

func httpMiddlewareFrom(value OSValue) *httpMiddleware {
	if value.Type != OSVObject || value.Opaque == nil {
		return nil
	}
	middleware, _ := (*Object)(value.Opaque).Native.(*httpMiddleware)
	return middleware
}

func (middleware *httpMiddleware) run(dispatch *httpDispatch, _ httpMatch) httpMiddlewareResult {
	if middleware.kind == "cookieParser" {
		dispatch.reqData.secrets = append(dispatch.reqData.secrets[:0], middleware.secrets...)
		httpParseSignedCookies(dispatch.request, dispatch.reqData)
		return httpMiddlewareResult{next: true}
	}

	if dispatch.reqData.bodyParsed {
		return httpMiddlewareResult{next: true}
	}
	if !httpTypeIs(dispatch.reqData.headers.Get("Content-Type"), []string{middleware.typeValue}) {
		return httpMiddlewareResult{next: true}
	}
	body := dispatch.reqData.body
	if middleware.limit > 0 && len(body) > middleware.limit {
		return httpMiddlewareResult{hasErr: true, err: httpErrorValue(http.StatusRequestEntityTooLarge, "request entity too large")}
	}
	var parsed OSValue
	switch middleware.kind {
	case "json":
		if middleware.strict {
			trimmed := strings.TrimSpace(body)
			if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
				return httpMiddlewareResult{hasErr: true, err: httpErrorValue(http.StatusBadRequest, "invalid JSON body")}
			}
		}
		parsed = nativeJSONParse(dispatch.vm, []OSValue{coreHTTPString(body)})
		if dispatch.vm.NativeError != "" {
			dispatch.vm.NativeError = ""
			return httpMiddlewareResult{hasErr: true, err: httpErrorValue(http.StatusBadRequest, "invalid JSON body")}
		}
	case "urlencoded":
		parser := "simple"
		if middleware.extended {
			parser = "extended"
		}
		parsed = httpValueObject(httpParseQuery(body, parser))
	case "text":
		parsed = coreHTTPString(body)
	case "raw":
		parsed = httpRawBody(body)
	default:
		return httpMiddlewareResult{next: true}
	}
	(*Object)(dispatch.request.Opaque).Set("body", parsed)
	dispatch.reqData.bodyParsed = true
	return httpMiddlewareResult{next: true}
}

func httpRawBody(body string) OSValue {
	bytes := make([]OSValue, len(body))
	for index := 0; index < len(body); index++ {
		bytes[index] = OSValue{Type: OSVInt, Number: float64(body[index])}
	}
	return httpArray(bytes)
}

func httpMiddlewareFactory(kind, typeValue string) func(*VM, []OSValue) OSValue {
	return func(vm *VM, args []OSValue) OSValue {
		middleware := &httpMiddleware{kind: kind, typeValue: typeValue, limit: 1 << 20, strict: true}
		if kind == "text" {
			middleware.typeValue = "text/*"
		}
		if kind == "raw" {
			middleware.typeValue = "application/octet-stream"
		}
		if kind == "cookieParser" {
			for _, arg := range args {
				if IsString(arg) && arg.StringValue() != "" {
					middleware.secrets = append(middleware.secrets, arg.StringValue())
				}
			}
		}
		if len(args) > 0 && args[0].Type == OSVObject && args[0].Opaque != nil {
			options := (*Object)(args[0].Opaque)
			if value, exists := options.Values["limit"]; exists {
				if IsNumber(value) && value.Number > 0 {
					middleware.limit = int(value.Number)
				} else if IsString(value) {
					if limit := httpParseDurationMillis(value); limit > 0 {
						middleware.limit = int(limit)
					}
				}
			}
			if value, exists := options.Values["strict"]; exists {
				middleware.strict = CoerceToBool(value)
			}
			if value, exists := options.Values["extended"]; exists {
				middleware.extended = CoerceToBool(value)
			}
			if value, exists := options.Values["type"]; exists && IsString(value) {
				middleware.typeValue = value.StringValue()
			}
		}
		return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(&Object{Native: middleware, Values: make(map[string]OSValue)})}
	}
}

func httpMiddlewareFactories() map[string]func(*VM, []OSValue) OSValue {
	return map[string]func(*VM, []OSValue) OSValue{
		"json":         httpMiddlewareFactory("json", "application/json"),
		"urlencoded":   httpMiddlewareFactory("urlencoded", "application/x-www-form-urlencoded"),
		"text":         httpMiddlewareFactory("text", "text/*"),
		"raw":          httpMiddlewareFactory("raw", "application/octet-stream"),
		"cookieParser": httpMiddlewareFactory("cookieParser", ""),
	}
}

func httpTypeIs(contentType string, expected []string) bool {
	if contentType == "" {
		return false
	}
	actual, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	actual = strings.ToLower(actual)
	for _, candidate := range expected {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "" {
			continue
		}
		if strings.Contains(candidate, "/") {
			if matchesHTTPMediaType(actual, candidate) {
				return true
			}
			continue
		}
		if extension, _, found := strings.Cut(candidate, "."); found {
			candidate = extension
		}
		if mediaType, _, err := mime.ParseMediaType(mime.TypeByExtension("." + candidate)); err == nil && mediaType != "" && matchesHTTPMediaType(actual, mediaType) {
			return true
		}
		if candidate == "json" && (actual == "application/json" || strings.HasSuffix(actual, "+json")) {
			return true
		}
		if candidate == "html" && actual == "text/html" {
			return true
		}
		if candidate == "text" && strings.HasPrefix(actual, "text/") {
			return true
		}
	}
	return false
}

func matchesHTTPMediaType(actual, expected string) bool {
	actualParts := strings.SplitN(actual, "/", 2)
	expectedParts := strings.SplitN(expected, "/", 2)
	return len(actualParts) == 2 && len(expectedParts) == 2 &&
		(expectedParts[0] == "*" || actualParts[0] == expectedParts[0]) &&
		(expectedParts[1] == "*" || actualParts[1] == expectedParts[1])
}

func httpSignCookie(value, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(value))
	signature := strings.TrimRight(base64.StdEncoding.EncodeToString(mac.Sum(nil)), "=")
	return "s:" + value + "." + signature
}

func httpParseSignedCookies(requestValue OSValue, data *httpRequestData) {
	if requestValue.Type != OSVObject || requestValue.Opaque == nil || len(data.secrets) == 0 {
		return
	}
	request := (*Object)(requestValue.Opaque)
	cookiesValue := request.Values["cookies"]
	if cookiesValue.Type != OSVObject || cookiesValue.Opaque == nil {
		return
	}
	cookies := (*Object)(cookiesValue.Opaque)
	signed := &Object{Values: make(map[string]OSValue)}
	for _, name := range append([]string(nil), cookies.Keys...) {
		value, exists := cookies.Values[name]
		if !exists || !IsString(value) || !strings.HasPrefix(value.StringValue(), "s:") {
			continue
		}
		payload := strings.TrimPrefix(value.StringValue(), "s:")
		plain, signature, found := strings.Cut(payload, ".")
		verified := false
		if found {
			for _, secret := range data.secrets {
				expected := strings.TrimPrefix(httpSignCookie(plain, secret), "s:"+plain+".")
				if hmac.Equal([]byte(expected), []byte(signature)) {
					verified = true
					break
				}
			}
		}
		if verified {
			signed.Set(name, coreHTTPString(plain))
			cookies.Set(name, OSValue{Type: OSVBool})
		} else {
			signed.Set(name, OSValue{Type: OSVBool})
		}
	}
	request.Set("signedCookies", OSValue{Type: OSVObject, Opaque: unsafe.Pointer(signed)})
}
