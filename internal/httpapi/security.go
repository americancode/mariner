package httpapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	csrfCookieName = "mariner_csrf"
	csrfHeaderName = "X-CSRF-Token"
	maxJSONBody    = 1 << 20
)

type rateWindow struct {
	started time.Time
	count   int
}

type rateLimiter struct {
	mu      sync.Mutex
	entries map[string]rateWindow
}

func (s *Server) securityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.NewString()
		w.Header().Set("X-Request-ID", requestID)
		started := time.Now()
		observed := &observedResponseWriter{ResponseWriter: w}
		defer func() {
			status := observed.status
			if status == 0 {
				status = http.StatusOK
			}
			slog.Info("HTTP request", "request_id", requestID, "status", status, "bytes", observed.bytes, "duration_ms", time.Since(started).Milliseconds())
		}()
		w = observed
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; frame-ancestors 'none'; object-src 'none'; form-action 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; font-src 'self' data:")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}

		csrf := r.Method == http.MethodGet || r.Method == http.MethodHead
		cookie, err := r.Cookie(csrfCookieName)
		if err != nil || !s.validCSRFToken(cookie.Value) {
			value, tokenErr := s.newCSRFToken()
			if tokenErr != nil {
				slog.Error("CSRF token initialization failed", "request_id", requestID)
				http.Error(w, "unable to initialize request security", http.StatusInternalServerError)
				return
			}
			cookie = &http.Cookie{Name: csrfCookieName, Value: value, Path: "/", Secure: true, SameSite: http.SameSiteStrictMode}
			http.SetCookie(w, cookie)
		}
		if !csrf && (strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/auth/logout") && !hmac.Equal([]byte(cookie.Value), []byte(r.Header.Get(csrfHeaderName))) {
			slog.Warn("CSRF validation failed", "request_id", requestID)
			http.Error(w, "missing or invalid CSRF token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type observedResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *observedResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *observedResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(body)
	w.bytes += int64(n)
	return n, err
}

func (w *observedResponseWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *observedResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) newCSRFToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(value)
	mac := hmac.New(sha256.New, []byte(s.csrfSecret()))
	_, _ = mac.Write([]byte(raw))
	return raw + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *Server) validCSRFToken(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(s.csrfSecret()))
	_, _ = mac.Write([]byte(parts[0]))
	return hmac.Equal(signature, mac.Sum(nil))
}

func (s *Server) csrfSecret() string {
	if s.Auth == nil {
		return ""
	}
	return s.Auth.CookieSecret
}

func DecodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request contains multiple JSON values")
		}
		return err
	}
	return nil
}

func (s *Server) sensitiveRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.RateLimitEnabled {
			next.ServeHTTP(w, r)
			return
		}
		limit := 0
		switch r.URL.Path {
		case "/auth/login", "/api/vault/unlock":
			if r.URL.Path == "/auth/login" {
				limit = s.LoginRateLimit
			} else {
				limit = s.UnlockRateLimit
			}
		case "/api/connections/test":
			limit = s.ConnectionTestRateLimit
		}
		window := s.RateLimitWindow
		if window <= 0 {
			window = time.Minute
		}
		if limit <= 0 || s.rateLimiterAllow(clientAddress(r), r.URL.Path, limit, window) {
			next.ServeHTTP(w, r)
			return
		}
		slog.Warn("rate limit exceeded", "limit", limit, "window", window.String())
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
	})
}

func clientAddress(r *http.Request) string {
	address, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return address
	}
	return r.RemoteAddr
}

func (s *Server) rateLimiterAllow(key, path string, limit int, window time.Duration) bool {
	now := time.Now()
	entryKey := key + "\x00" + path
	s.limiter.mu.Lock()
	defer s.limiter.mu.Unlock()
	entry := s.limiter.entries[entryKey]
	if entry.started.IsZero() || now.Sub(entry.started) >= window {
		s.limiter.entries[entryKey] = rateWindow{started: now, count: 1}
		return true
	}
	if entry.count >= limit {
		return false
	}
	entry.count++
	s.limiter.entries[entryKey] = entry
	return true
}

func validateObjectName(value string) error {
	if value == "" {
		return errors.New("object name is required")
	}
	if utf8.RuneCountInString(value) > 4096 {
		return errors.New("object name is too long")
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return errors.New("object name contains control characters")
	}
	return nil
}
