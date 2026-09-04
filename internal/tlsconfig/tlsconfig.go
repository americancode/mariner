package tlsconfig

import (
	"crypto/x509"
	"encoding/pem"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// RootCAs returns the platform trust pool with custom certificates appended.
// SSL_CERT_FILE may identify one PEM file; SSL_CERT_DIR may contain a
// platform-separated list of directories whose regular files are read.
func RootCAs() (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if file := os.Getenv("SSL_CERT_FILE"); file != "" {
		if err := appendFile(pool, file); err != nil {
			return nil, err
		}
	}
	for _, dir := range filepath.SplitList(os.Getenv("SSL_CERT_DIR")) {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			// Kubernetes Secret and ConfigMap projections include metadata
			// entries such as ..data and ..2026_09_04_... . They are not
			// certificate files; ..data is a directory reached through a
			// symlink, so attempting to read it as a file fails.
			if strings.HasPrefix(entry.Name(), "..") {
				continue
			}
			if entry.IsDir() {
				continue
			}
			if err := appendFile(pool, filepath.Join(dir, entry.Name())); err != nil {
				return nil, err
			}
		}
	}
	return pool, nil
}

func appendFile(pool *x509.CertPool, file string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	certs := 0
	for rest := data; len(rest) > 0; {
		block, remaining := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = remaining
		if block.Type == "CERTIFICATE" {
			certs++
		}
	}
	// Secret and ConfigMap objects may contain non-certificate files (for
	// example tls.key). Ignore those files; only PEM certificates belong in
	// the trust pool.
	if certs == 0 {
		return nil
	}
	if !pool.AppendCertsFromPEM(data) {
		return os.ErrInvalid
	}
	if strings.TrimSpace(string(data)) != "" {
		log.Printf("tls: appended %d certificates from %s", certs, file)
	}
	return nil
}
