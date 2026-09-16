package config

import (
	"testing"
	"time"
)

func TestLoadSessionIdleTimeout(t *testing.T) {
	t.Setenv("SESSION_IDLE_TIMEOUT", "")
	if got := Load().SessionIdleTimeout; got != 30*time.Minute {
		t.Fatalf("default session idle timeout = %s, want 30m", got)
	}

	t.Setenv("SESSION_IDLE_TIMEOUT", "45m")
	if got := Load().SessionIdleTimeout; got != 45*time.Minute {
		t.Fatalf("configured session idle timeout = %s, want 45m", got)
	}
	t.Setenv("MULTIPART_CLEANUP_INTERVAL", "5m")
	t.Setenv("MULTIPART_CLEANUP_MAX_AGE", "12h")
	if got := Load(); got.MultipartCleanupInterval != 5*time.Minute || got.MultipartCleanupMaxAge != 12*time.Hour || !got.MultipartCleanupEnabled {
		t.Fatalf("unexpected multipart cleanup configuration: %+v", got)
	}
}
