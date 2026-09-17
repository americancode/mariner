package auth

import "testing"

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
