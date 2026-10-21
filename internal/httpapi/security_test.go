package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityMiddlewareSetsHeadersAndCSRFToken(t *testing.T) {
	server := &Server{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	response := httptest.NewRecorder()
	server.securityMiddleware(next).ServeHTTP(response, request)

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := response.Header().Get(header); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}
	cookie := response.Result().Cookies()
	if len(cookie) != 1 || cookie[0].Name != csrfCookieName || !server.validCSRFToken(cookie[0].Value) {
		t.Fatalf("expected a valid CSRF cookie, got %#v", cookie)
	}
}

func TestSecurityMiddlewareRequiresCSRFForAPIWrites(t *testing.T) {
	server := &Server{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := server.securityMiddleware(next)

	withoutToken := httptest.NewRecorder()
	handler.ServeHTTP(withoutToken, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{}`)))
	if withoutToken.Code != http.StatusForbidden {
		t.Fatalf("status without CSRF token = %d, want %d", withoutToken.Code, http.StatusForbidden)
	}

	seed := httptest.NewRecorder()
	handler.ServeHTTP(seed, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	cookie := seed.Result().Cookies()[0]
	withToken := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{}`))
	withToken.AddCookie(cookie)
	withToken.Header.Set(csrfHeaderName, cookie.Value)
	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, withToken)
	if allowed.Code != http.StatusNoContent {
		t.Fatalf("status with CSRF token = %d, want %d", allowed.Code, http.StatusNoContent)
	}
}

func TestDecodeJSONRejectsUnknownAndTrailingValues(t *testing.T) {
	for _, body := range []string{`{"theme":"light","unexpected":true}`, `{"theme":"light"}{"theme":"dark"}`} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
		var value struct {
			Theme string `json:"theme"`
		}
		if DecodeJSON(response, request, &value) == nil {
			t.Fatalf("DecodeJSON accepted %q", body)
		}
	}
}
