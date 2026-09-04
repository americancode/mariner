package tlsconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRootCAsIgnoresKubernetesProjectionMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "..data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tls.key"), []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", "")
	t.Setenv("SSL_CERT_DIR", dir)

	if _, err := RootCAs(); err != nil {
		t.Fatalf("RootCAs() returned an error for projected metadata: %v", err)
	}
}
