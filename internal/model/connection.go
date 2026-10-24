package model

import (
	"errors"
	"strings"
)

// Connection is the domain representation of an S3 connection. Credentials
// are intentionally part of the backend model only; HTTP response DTOs must
// project them out before serialization.
type Connection struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Bucket    string `json:"bucket"`
	Region    string `json:"region"`
	Endpoint  string `json:"endpoint"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	Prefix    string `json:"prefix"`
	GroupPath string `json:"groupPath,omitempty"`
	// Organization is empty for a personal-vault connection. It is the
	// organization ID for a shared connection.
	Organization          string `json:"organization,omitempty"`
	OrganizationCanManage bool   `json:"-"`
}

const MaxConnectionGroupDepth = 5

func ValidateConnectionGroupPath(path string) error {
	if path == "" {
		return nil
	}
	parts := strings.Split(path, "/")
	if len(parts) > MaxConnectionGroupDepth {
		return errors.New("connection group nesting cannot exceed five levels")
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "." || part == ".." {
			return errors.New("connection group names must be non-empty and safe")
		}
		if strings.ContainsAny(part, "\\\r\n\t") {
			return errors.New("connection group names contain invalid characters")
		}
	}
	return nil
}

func ConnectionGroupContains(parent, child string) bool {
	return child == parent || (parent != "" && strings.HasPrefix(child, parent+"/"))
}

// NewConnection creates a connection with the minimum business invariants
// required by the application. Transport-specific validation belongs in the
// HTTP DTO mapper; S3 reachability belongs in the S3 service.
func NewConnection(name, bucket string) (Connection, error) {
	name = strings.TrimSpace(name)
	bucket = strings.TrimSpace(bucket)
	if name == "" {
		return Connection{}, errors.New("connection name is required")
	}
	if bucket == "" {
		return Connection{}, errors.New("bucket is required")
	}
	return Connection{Name: name, Bucket: bucket}, nil
}
