package httpapi

import (
	"testing"

	"mariner/internal/model"
)

func TestOrderedUploadPartsSortsConcurrentParts(t *testing.T) {
	parts, err := orderedUploadParts(map[int32]model.UploadPart{
		3: {PartNumber: 3, ETag: "three"},
		1: {PartNumber: 1, ETag: "one"},
		2: {PartNumber: 2, ETag: "two"},
	})
	if err != nil {
		t.Fatalf("orderedUploadParts returned error: %v", err)
	}
	for i, want := range []string{"one", "two", "three"} {
		if parts[i].ETag != want || parts[i].PartNumber != int32(i+1) {
			t.Fatalf("parts[%d]=%+v, want part %d/%s", i, parts[i], i+1, want)
		}
	}
}

func TestOrderedUploadPartsRejectsMissingPart(t *testing.T) {
	_, err := orderedUploadParts(map[int32]model.UploadPart{
		1: {PartNumber: 1, ETag: "one"},
		3: {PartNumber: 3, ETag: "three"},
	})
	if err == nil || err.Error() != "multipart parts are incomplete" {
		t.Fatalf("error=%v, want incomplete multipart error", err)
	}
}

func TestUploadSettingsFallbacks(t *testing.T) {
	s := &Server{}
	if got := s.uploadPartSize(); got != 16*1024*1024 {
		t.Fatalf("part size=%d, want 16 MiB", got)
	}
	if got := s.uploadPartConcurrency(); got != 1 {
		t.Fatalf("concurrency=%d, want 1", got)
	}
	s.UploadPartSize = 8 * 1024 * 1024
	s.UploadPartConcurrency = 3
	if s.uploadPartSize() != 8*1024*1024 || s.uploadPartConcurrency() != 3 {
		t.Fatalf("configured settings were not honored: %d/%d", s.uploadPartSize(), s.uploadPartConcurrency())
	}
}
