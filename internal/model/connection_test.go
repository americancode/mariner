package model

import "testing"

func TestNewConnectionRequiresNameAndBucket(t *testing.T) {
	for _, test := range []struct {
		name   string
		bucket string
	}{
		{"", "bucket"},
		{"name", ""},
	} {
		if _, err := NewConnection(test.name, test.bucket); err == nil {
			t.Fatalf("NewConnection(%q, %q) accepted invalid input", test.name, test.bucket)
		}
	}
}

func TestNewConnectionTrimsRequiredFields(t *testing.T) {
	connection, err := NewConnection("  Demo  ", "  bucket  ")
	if err != nil {
		t.Fatalf("NewConnection returned error: %v", err)
	}
	if connection.Name != "Demo" || connection.Bucket != "bucket" {
		t.Fatalf("connection = %+v, want trimmed required fields", connection)
	}
}
