package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestHTTPAllowedMethods(t *testing.T) {
	layers := []httpLayer{
		{method: http.MethodGet, pattern: []string{"users", ":id"}},
		{method: http.MethodPost, pattern: []string{"users", ":id"}},
		{method: http.MethodGet, pattern: []string{"health"}},
		{method: http.MethodDelete, pattern: []string{"users", ":id"}, prefix: true},
	}

	got := httpAllowedMethods(layers, []string{"users", "42"})
	want := []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodOptions}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("httpAllowedMethods() = %v, want %v", got, want)
	}
}

func TestHTTPAllowedMethodsNoRoute(t *testing.T) {
	layers := []httpLayer{{method: http.MethodGet, pattern: []string{"users", ":id"}}}
	if got := httpAllowedMethods(layers, []string{"health"}); len(got) != 0 {
		t.Fatalf("httpAllowedMethods() = %v, want no methods", got)
	}
}

func TestHTTPAutomaticOptions(t *testing.T) {
	state := &httpAppState{
		layers: []httpLayer{
			{method: http.MethodGet, pattern: []string{"users", ":id"}},
			{method: http.MethodPost, pattern: []string{"users", ":id"}},
		},
	}
	request := httptest.NewRequest(http.MethodOptions, "/users/42", nil)
	response := httptest.NewRecorder()
	state.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if got, want := response.Header().Get("Allow"), "GET, HEAD, POST, OPTIONS"; got != want {
		t.Fatalf("Allow header = %q, want %q", got, want)
	}
}

func TestHTTPCompiledPathMatcher(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    map[string]string
		match   bool
	}{
		{pattern: "/users/:id(\\d+)", path: "/users/42", want: map[string]string{"id": "42"}, match: true},
		{pattern: "/users/:id?", path: "/users", want: map[string]string{}, match: true},
		{pattern: "/users/:parts+", path: "/users/a/b", want: map[string]string{"parts": "a/b"}, match: true},
		{pattern: "/users/:parts*", path: "/users", want: map[string]string{}, match: true},
		{pattern: "/files/*/edit", path: "/files/a/b/edit", want: map[string]string{"0": "a/b"}, match: true},
		{pattern: "/users/:id(\\d+)", path: "/users/nope", match: false},
	}
	for _, test := range tests {
		matcher, err := compileHTTPPath(splitPath(test.pattern))
		if err != nil {
			t.Fatalf("compileHTTPPath(%q): %v", test.pattern, err)
		}
		got, matched := matcher.match(test.path)
		if matched != test.match || !reflect.DeepEqual(got, test.want) {
			t.Errorf("match(%q) = (%v, %v), want (%v, %v)", test.path, got, matched, test.want, test.match)
		}
	}
}

func TestHTTPCompiledPrefixMatcher(t *testing.T) {
	matcher, err := compileHTTPPath([]string{"api", ":version"})
	if err != nil {
		t.Fatal(err)
	}
	got, matched := matcher.matchPrefix("/api/v1/users")
	if !matched || !reflect.DeepEqual(got, map[string]string{"version": "v1"}) {
		t.Fatalf("matchPrefix() = (%v, %v), want (map[version:v1], true)", got, matched)
	}
}

func TestHTTPSplitRequestPathRejectsMalformedEscapes(t *testing.T) {
	if _, err := splitRequestPath("/users/%zz"); err == nil {
		t.Fatal("splitRequestPath accepted malformed percent-encoding")
	}
}

func TestHTTPMalformedPathReturnsBadRequest(t *testing.T) {
	state := &httpAppState{layers: []httpLayer{{method: http.MethodGet, pattern: []string{"users", ":id"}}}}
	request := httptest.NewRequest(http.MethodGet, "/users/ok", nil)
	request.URL.RawPath = "/users/%zz"
	request.URL.Path = "/users/%zz"
	response := httptest.NewRecorder()
	state.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "malformed path encoding") {
		t.Fatalf("malformed path response = (%d, %q), want 400 malformed path encoding", response.Code, response.Body.String())
	}
}

func TestHTTPETagMatchesWeakAndWildcardValidators(t *testing.T) {
	current := `W/"abc"`
	for _, value := range []string{`"abc"`, current, `"other", "abc"`, `*`} {
		if !httpETagMatches([]string{value}, current) {
			t.Errorf("httpETagMatches(%q, %q) = false, want true", value, current)
		}
	}
	if httpETagMatches([]string{`"other"`}, current) {
		t.Fatal("httpETagMatches accepted a different validator")
	}
}
