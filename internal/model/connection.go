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
