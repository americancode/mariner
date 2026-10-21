package s3service

import (
	"context"
	"testing"

	"mariner/internal/model"
)

func TestSafeArchivePath(t *testing.T) {
	for _, path := range []string{"file.txt", "folder/file.txt"} {
		if err := safeArchivePath(path); err != nil {
			t.Errorf("safeArchivePath(%q) returned error: %v", path, err)
		}
	}
	for _, path := range []string{"/file.txt", "../file.txt", "folder/../../file.txt"} {
		if err := safeArchivePath(path); err == nil {
			t.Errorf("safeArchivePath(%q) accepted unsafe path", path)
		}
	}
}

func TestNewWithUploadSettingsUsesConfiguredValues(t *testing.T) {
	service := NewWithUploadSettings(8*1024*1024, 3)
	if service.partSize() != 8*1024*1024 || service.partConcurrency() != 3 {
		t.Fatalf("settings=%d/%d, want 8 MiB/3", service.partSize(), service.partConcurrency())
	}
}

func TestNewWithUploadSettingsUsesSafeDefaults(t *testing.T) {
	service := NewWithUploadSettings(0, 0)
	if service.partSize() != MultipartPartSize || service.partConcurrency() != 1 {
		t.Fatalf("settings=%d/%d, want defaults %d/1", service.partSize(), service.partConcurrency(), MultipartPartSize)
	}
}

func TestS3ClientIsReusedForSameConnection(t *testing.T) {
	service := New()
	connection := model.Connection{ID: "demo", Name: "Demo", Bucket: "bucket", Region: "us-east-1", AccessKey: "access", SecretKey: "secret"}
	first, err := service.client(context.Background(), connection)
	if err != nil {
		t.Fatalf("first client: %v", err)
	}
	second, err := service.client(context.Background(), connection)
	if err != nil {
		t.Fatalf("second client: %v", err)
	}
	if first != second {
		t.Fatal("same connection created multiple S3 clients")
	}
}
