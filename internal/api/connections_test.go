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

type fakeOrganizationRepository struct{ organizations map[string]model.Organization }

func (f *fakeOrganizationRepository) List() (map[string]model.Organization, error) {
	return f.organizations, nil
}
func (f *fakeOrganizationRepository) Save(organization model.Organization) error {
	if f.organizations == nil {
		f.organizations = map[string]model.Organization{}
	}
	f.organizations[organization.ID] = organization
	return nil
}
func (f *fakeOrganizationRepository) Delete(id string) error { delete(f.organizations, id); return nil }

func (f *fakeConnectionTester) TestConnection(_ context.Context, connection model.Connection) error {
	f.last = connection
	return f.err
}

func TestConnectionAPICreatePersistsAfterS3Validation(t *testing.T) {
	vault := &fakeVaultRepository{}
	tester := &fakeConnectionTester{}
	service := NewConnectionAPI(vault, tester, func() string { return "connection-1" }, nil)

	id, err := service.Create(context.Background(), "user-1", "password", nil, model.Connection{Name: "Demo", Bucket: "bucket"})
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

func TestConnectionAPICreatePersistsGroupPath(t *testing.T) {
	vault := &fakeVaultRepository{}
	service := NewConnectionAPI(vault, &fakeConnectionTester{}, func() string { return "connection-1" }, nil)
	if _, err := service.Create(context.Background(), "user-1", "password", nil, model.Connection{Name: "Demo", Bucket: "bucket", GroupPath: "Engineering/Production"}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if vault.data.Connections[0].GroupPath != "Engineering/Production" {
		t.Fatalf("saved connection = %+v", vault.data.Connections[0])
	}
}

func TestConnectionAPIRejectsGroupPathDeeperThanFiveLevels(t *testing.T) {
	service := NewConnectionAPI(&fakeVaultRepository{}, &fakeConnectionTester{}, func() string { return "unused" }, nil)
	if _, err := service.Create(context.Background(), "user-1", "password", nil, model.Connection{Name: "Demo", Bucket: "bucket", GroupPath: "one/two/three/four/five/six"}); err == nil {
		t.Fatal("Create accepted a group path deeper than five levels")
	}
}

func TestConnectionAPIUpdatePreservesOmittedSecrets(t *testing.T) {
	vault := &fakeVaultRepository{data: model.Data{Connections: []model.Connection{{
		ID: "connection-1", Name: "Old", Bucket: "bucket", AccessKey: "access", SecretKey: "secret",
	}}}}
	service := NewConnectionAPI(vault, &fakeConnectionTester{}, func() string { return "unused" }, nil)

	err := service.Update(context.Background(), "user-1", "password", nil, model.Connection{
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

func TestConnectionAPIRejectsOrganizationMemberWithoutMembership(t *testing.T) {
	service := NewConnectionAPI(&fakeVaultRepository{}, &fakeConnectionTester{}, func() string { return "unused" }, nil)
	service.ResolveOrganization = func([]string, string) (model.Organization, error) {
		return model.Organization{}, ErrOrganizationMembership
	}
	err := service.Update(context.Background(), "user-1", "password", nil, model.Connection{ID: "org:connection-1", Name: "Org", Bucket: "bucket"})
	if !errors.Is(err, ErrOrganizationMembership) {
		t.Fatalf("error = %v, want membership error", err)
	}
}

func TestConnectionAPICreateDoesNotPersistWhenS3ValidationFails(t *testing.T) {
	vault := &fakeVaultRepository{}
	service := NewConnectionAPI(vault, &fakeConnectionTester{err: errors.New("unreachable")}, func() string { return "unused" }, nil)

	if _, err := service.Create(context.Background(), "user-1", "password", nil, model.Connection{Name: "Demo", Bucket: "bucket"}); err == nil {
		t.Fatal("Create succeeded despite S3 validation failure")
	}
	if len(vault.data.Connections) != 0 {
		t.Fatalf("vault data = %+v, connection was persisted after validation failure", vault.data)
	}
}

func TestConnectionAPIMemberCanCreateAndDeleteOrganizationConnection(t *testing.T) {
	organizations := &fakeOrganizationRepository{organizations: map[string]model.Organization{
		"team": {ID: "team", Name: "Team", Groups: []string{"members"}},
	}}
	service := NewConnectionAPI(&fakeVaultRepository{}, &fakeConnectionTester{}, func() string { return "connection-1" }, nil)
	service.Organizations = organizations
	service.ResolveOrganization = func(groups []string, id string) (model.Organization, error) {
		org := organizations.organizations[id]
		for _, group := range groups {
			for _, required := range org.Groups {
				if group == required {
					return org, nil
				}
			}
		}
		return model.Organization{}, ErrOrganizationMembership
	}
	id, err := service.Create(context.Background(), "user-1", "password", []string{"members"}, model.Connection{Name: "Shared", Bucket: "bucket", Organization: "team"})
	if err != nil || id != "team:connection-1" {
		t.Fatalf("create id=%q err=%v", id, err)
	}
	if _, ok := organizations.organizations["team"].Connections["connection-1"]; !ok {
		t.Fatal("organization connection was not saved")
	}
	if err := service.Delete("user-1", "password", []string{"members"}, id); err != nil {
		t.Fatalf("delete returned error: %v", err)
	}
	if len(organizations.organizations["team"].Connections) != 0 {
		t.Fatal("organization connection was not deleted")
	}
}

func TestConnectionAPIMovesBetweenPersonalAndOrganizationVaults(t *testing.T) {
	vault := &fakeVaultRepository{data: model.Data{Connections: []model.Connection{{ID: "local", Name: "Shared", Bucket: "bucket", AccessKey: "a", SecretKey: "s"}}}}
	organizations := &fakeOrganizationRepository{organizations: map[string]model.Organization{"team": {ID: "team", Name: "Team", Groups: []string{"members"}}}}
	service := NewConnectionAPI(vault, &fakeConnectionTester{}, func() string { return "unused" }, nil)
	service.Organizations = organizations
	service.ResolveOrganization = func(groups []string, id string) (model.Organization, error) {
		if id != "team" || len(groups) == 0 {
			return model.Organization{}, ErrOrganizationMembership
		}
		return organizations.organizations[id], nil
	}
	update := model.Connection{ID: "local", Name: "Shared", Bucket: "bucket", Organization: "team"}
	if err := service.Update(context.Background(), "user-1", "password", []string{"members"}, update); err != nil {
		t.Fatalf("personal to organization: %v", err)
	}
	if len(vault.data.Connections) != 0 || len(organizations.organizations["team"].Connections) != 1 {
		t.Fatalf("personal to organization state: vault=%+v org=%+v", vault.data, organizations.organizations["team"])
	}
	if err := service.Update(context.Background(), "user-1", "password", []string{"members"}, model.Connection{ID: "team:local", Name: "Personal", Bucket: "bucket"}); err != nil {
		t.Fatalf("organization to personal: %v", err)
	}
	if len(vault.data.Connections) != 1 || len(organizations.organizations["team"].Connections) != 0 || vault.data.Connections[0].Name != "Personal" {
		t.Fatalf("organization to personal state: vault=%+v org=%+v", vault.data, organizations.organizations["team"])
	}
}

func TestConnectionAPIMovesWithOmittedCredentials(t *testing.T) {
	vault := &fakeVaultRepository{data: model.Data{Connections: []model.Connection{{
		ID: "local", Name: "Shared", Bucket: "bucket", AccessKey: "saved-access", SecretKey: "saved-secret",
	}}}}
	organizations := &fakeOrganizationRepository{organizations: map[string]model.Organization{
		"team": {ID: "team", Name: "Team", Groups: []string{"members"}},
	}}
	tester := &fakeConnectionTester{}
	service := NewConnectionAPI(vault, tester, func() string { return "unused" }, nil)
	service.Organizations = organizations
	service.ResolveOrganization = func(groups []string, id string) (model.Organization, error) {
		if id != "team" || len(groups) == 0 {
			return model.Organization{}, ErrOrganizationMembership
		}
		return organizations.organizations[id], nil
	}

	if err := service.Update(context.Background(), "user-1", "password", []string{"members"}, model.Connection{
		ID: "local", Name: "Shared", Bucket: "bucket", Organization: "team",
	}); err != nil {
		t.Fatalf("move returned error: %v", err)
	}
	shared := organizations.organizations["team"].Connections["local"].Connection
	if shared.AccessKey != "saved-access" || shared.SecretKey != "saved-secret" {
		t.Fatalf("moved connection = %+v, saved credentials were not preserved", shared)
	}
	if tester.last.AccessKey != "saved-access" || tester.last.SecretKey != "saved-secret" {
		t.Fatalf("tested connection = %+v, saved credentials were not used", tester.last)
	}
}
