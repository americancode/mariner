package api

import (
	"context"
	"errors"
	"testing"

	"mariner/internal/model"
)

type fakeVaultRepository struct {
	data model.Data
}

func (f *fakeVaultRepository) Load(string, string) (model.Data, bool, error) {
	return f.data, true, nil
}

func (f *fakeVaultRepository) Save(_ string, _ string, data model.Data) error {
	f.data = data
	return nil
}

type fakeConnectionTester struct {
	last model.Connection
	err  error
}

func (f *fakeConnectionTester) TestConnection(_ context.Context, connection model.Connection) error {
	f.last = connection
	return f.err
}

func TestConnectionAPICreatePersistsAfterS3Validation(t *testing.T) {
	vault := &fakeVaultRepository{}
	tester := &fakeConnectionTester{}
	service := NewConnectionAPI(vault, tester, func() string { return "connection-1" }, nil)

	id, err := service.Create(context.Background(), "user-1", "password", model.Connection{Name: "Demo", Bucket: "bucket"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if id != "connection-1" || len(vault.data.Connections) != 1 {
		t.Fatalf("id=%q data=%+v, want one persisted connection", id, vault.data)
	}
	if tester.last.Bucket != "bucket" {
		t.Fatalf("tested connection = %+v", tester.last)
	}
}

func TestConnectionAPIUpdatePreservesOmittedSecrets(t *testing.T) {
	vault := &fakeVaultRepository{data: model.Data{Connections: []model.Connection{{
		ID: "connection-1", Name: "Old", Bucket: "bucket", AccessKey: "access", SecretKey: "secret",
	}}}}
	service := NewConnectionAPI(vault, &fakeConnectionTester{}, func() string { return "unused" }, nil)

	err := service.Update(context.Background(), "user-1", "password", model.Connection{
		ID: "connection-1", Name: "New", Bucket: "bucket",
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	updated := vault.data.Connections[0]
	if updated.Name != "New" || updated.AccessKey != "access" || updated.SecretKey != "secret" {
		t.Fatalf("updated connection = %+v, omitted secrets were not preserved", updated)
	}
}

func TestConnectionAPICannotModifyOrganizationConnection(t *testing.T) {
	service := NewConnectionAPI(&fakeVaultRepository{}, &fakeConnectionTester{}, func() string { return "unused" }, func(id string) bool { return id == "org:connection-1" })

	err := service.Update(context.Background(), "user-1", "password", model.Connection{
		ID: "org:connection-1", Name: "Org", Bucket: "bucket",
	})
	if !errors.Is(err, ErrOrganizationConnection) {
		t.Fatalf("error = %v, want ErrOrganizationConnection", err)
	}
}

func TestConnectionAPICreateDoesNotPersistWhenS3ValidationFails(t *testing.T) {
	vault := &fakeVaultRepository{}
	service := NewConnectionAPI(vault, &fakeConnectionTester{err: errors.New("unreachable")}, func() string { return "unused" }, nil)

	if _, err := service.Create(context.Background(), "user-1", "password", model.Connection{Name: "Demo", Bucket: "bucket"}); err == nil {
		t.Fatal("Create succeeded despite S3 validation failure")
	}
	if len(vault.data.Connections) != 0 {
		t.Fatalf("vault data = %+v, connection was persisted after validation failure", vault.data)
	}
}
