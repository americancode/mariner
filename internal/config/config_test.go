package config

import "testing"

func TestLoadUsesSafeMultipartDefaults(t *testing.T) {
	for _, key := range []string{"UPLOAD_PART_SIZE", "UPLOAD_PART_CONCURRENCY", "DATABASE_DRIVER", "DATABASE_URL"} {
		t.Setenv(key, "")
	}
	cfg := Load()
	if cfg.UploadPartSize != 16*1024*1024 {
		t.Fatalf("UploadPartSize=%d, want 16 MiB", cfg.UploadPartSize)
	}
	if cfg.UploadPartConcurrency != 1 {
		t.Fatalf("UploadPartConcurrency=%d, want 1", cfg.UploadPartConcurrency)
	}
}

func TestLoadAcceptsConfiguredMultipartSettings(t *testing.T) {
	t.Setenv("UPLOAD_PART_SIZE", "16777216")
	t.Setenv("UPLOAD_PART_CONCURRENCY", "1")
	cfg := Load()
	if cfg.UploadPartSize != 16777216 || cfg.UploadPartConcurrency != 1 {
		t.Fatalf("multipart settings=%d/%d, want 16777216/1", cfg.UploadPartSize, cfg.UploadPartConcurrency)
	}
}

func TestLoadRejectsInvalidMultipartSettingsByUsingDefaults(t *testing.T) {
	t.Setenv("UPLOAD_PART_SIZE", "-1")
	t.Setenv("UPLOAD_PART_CONCURRENCY", "not-a-number")
	cfg := Load()
	if cfg.UploadPartSize != 16*1024*1024 || cfg.UploadPartConcurrency != 1 {
		t.Fatalf("multipart settings=%d/%d, want defaults 16 MiB/1", cfg.UploadPartSize, cfg.UploadPartConcurrency)
	}
}
