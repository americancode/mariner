package model

import (
	"encoding/json"
	"testing"
)

func TestObjectItemJSONIncludesFileName(t *testing.T) {
	raw, err := json.Marshal(ObjectItem{Name: "report.pdf", Key: "docs/report.pdf", Kind: ObjectKindFile})
	if err != nil {
		t.Fatalf("marshal object: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal object: %v", err)
	}
	if decoded["name"] != "report.pdf" {
		t.Fatalf("JSON name=%v, want report.pdf", decoded["name"])
	}
}
