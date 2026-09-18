package auth

import (
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionSecretEncryptionRoundTrip(t *testing.T) {
	encoded := encryptSessionSecret("master-password", "cookie-secret")
	if encoded == "" {
		t.Fatal("expected encrypted session secret")
	}
	if encoded == "master-password" {
		t.Fatal("session secret must not be stored in plaintext")
	}
	decoded, err := decryptSessionSecret(encoded, "cookie-secret")
	if err != nil || decoded != "master-password" {
		t.Fatalf("decrypt: %q err=%v", decoded, err)
	}
	if _, err := decryptSessionSecret(encoded, "different-secret"); err == nil {
		t.Fatal("expected decryption with a different secret to fail")
	}
}

func TestSessionSecretEmptyValue(t *testing.T) {
	encoded := encryptSessionSecret("", "cookie-secret")
	decoded, err := decryptSessionSecret(encoded, "cookie-secret")
	if err != nil || decoded != "" {
		t.Fatalf("empty secret: %q err=%v", decoded, err)
	}
}

func TestSafeReturnPath(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{input: "/admin/audit", expected: "/admin/audit"},
		{input: "/admin/audit?offset=10", expected: "/admin/audit?offset=10"},
		{input: "https://evil.example/steal", expected: "/"},
		{input: "//evil.example/steal", expected: "/"},
		{input: "/\\\\evil.example/steal", expected: "/"},
	}
	for _, test := range tests {
		if got := safeReturnPath(test.input); got != test.expected {
			t.Errorf("safeReturnPath(%q) = %q, want %q", test.input, got, test.expected)
		}
	}
}

type countingSessionStore struct {
	touches atomic.Int32
	seen    time.Time
}

func (s *countingSessionStore) SaveSession(string, string, string, string, string, time.Time) error {
	return nil
}
func (s *countingSessionStore) LoadSession(string) (string, string, string, string, time.Time, time.Time, bool, error) {
	return "user-1", "Demo", `[]`, "", time.Now().Add(time.Hour), s.seen, true, nil
}
func (s *countingSessionStore) DeleteSession(string) error { return nil }
func (s *countingSessionStore) TouchSessionIfStale(string, time.Time, time.Time) error {
	s.touches.Add(1)
	return nil
}

func TestConcurrentRequestsTouchSharedSessionOnce(t *testing.T) {
	store := &countingSessionStore{seen: time.Now().Add(-time.Minute)}
	service := &Service{
		CookieSecret:       "test-secret",
		SessionIdleTimeout: time.Hour,
		sessionStore:       store,
		sessions:           map[string]Session{},
		lastTouches:        map[string]time.Time{},
	}
	recorder := httptest.NewRecorder()
	service.setCookie(recorder, "mariner_session", "session-1", 60)
	cookie := recorder.Result().Cookies()[0]
	const requests = 64
	var wg sync.WaitGroup
	for range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest("GET", "/api/me", nil)
			r.AddCookie(cookie)
			if _, _, ok := service.Current(r); !ok {
				t.Error("expected valid session")
			}
		}()
	}
	wg.Wait()
	if got := store.touches.Load(); got != 1 {
		t.Fatalf("concurrent requests performed %d session writes, want 1", got)
	}
}
