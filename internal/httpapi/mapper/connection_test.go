package mapper

import (
	"encoding/json"
	"strings"
	"testing"

	"mariner/internal/httpapi/dto"
	"mariner/internal/model"
)

func TestConnectionFromRequestPreservesWriteFields(t *testing.T) {
	connection, err := ConnectionFromRequest(dto.ConnectionRequest{
		ID: "connection-1", Name: "Demo", Bucket: "bucket", Region: "us-east-1",
		Endpoint: "https://s3.example.test", AccessKey: "access", SecretKey: "secret", Prefix: "prefix/",
		GroupPath: "Engineering/Production",
	})
	if err != nil {
		t.Fatalf("ConnectionFromRequest returned error: %v", err)
	}
	if connection.ID != "connection-1" || connection.SecretKey != "secret" || connection.Prefix != "prefix/" || connection.GroupPath != "Engineering/Production" {
		t.Fatalf("connection = %+v, missing write fields", connection)
	}
}

func TestConnectionToResponseRedactsCredentials(t *testing.T) {
	response := ConnectionToResponse(model.Connection{
		ID: "connection-1", Name: "Demo", Bucket: "bucket", AccessKey: "access", SecretKey: "secret",
	})
	if response.ID != "connection-1" || response.Bucket != "bucket" {
		t.Fatalf("response = %+v, missing public fields", response)
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if strings.Contains(string(raw), "access") || strings.Contains(string(raw), "secret") {
		t.Fatalf("response contains credentials: %s", raw)
	}
}
