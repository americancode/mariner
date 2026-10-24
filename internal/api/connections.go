package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"mariner/internal/model"
	"mariner/internal/repository"
)

type ConnectionTester interface {
	TestConnection(context.Context, model.Connection) error
}

type ConnectionAPI struct {
	Vault               repository.VaultRepository
	Tester              ConnectionTester
	NewID               func() string
	Organizations       repository.OrganizationRepository
	ResolveOrganization func(groups []string, id string) (model.Organization, error)
}

func NewConnectionAPI(vault repository.VaultRepository, tester ConnectionTester, newID func() string, _ func(string) bool) *ConnectionAPI {
	return &ConnectionAPI{Vault: vault, Tester: tester, NewID: newID}
}

func (a *ConnectionAPI) List(userID, password string) ([]model.Connection, error) {
	data, _, err := a.Vault.Load(userID, password)
	if err != nil {
		return nil, err
	}
	return append([]model.Connection(nil), data.Connections...), nil
}

func (a *ConnectionAPI) Create(ctx context.Context, userID, password string, groups []string, connection model.Connection) (string, error) {
	if err := validateConnection(connection); err != nil {
		return "", err
	}
	var organization model.Organization
	var err error
	if connection.Organization != "" {
		organization, err = a.organization(groups, connection.Organization)
		if err != nil {
			return "", err
		}
	}
	if err := a.Tester.TestConnection(ctx, connection); err != nil {
		return "", fmt.Errorf("connection test failed: %w", err)
	}
	connection.ID = a.NewID()
	if connection.Organization != "" {
		org := organization
		if org.Connections == nil {
			org.Connections = map[string]model.OrganizationConnection{}
		}
		connection.Organization = ""
		org.Connections[connection.ID] = model.OrganizationConnection{Connection: connection}
		if err := a.saveOrganization(org); err != nil {
			return "", err
		}
		return org.ID + ":" + connection.ID, nil
	}
	data, _, err := a.Vault.Load(userID, password)
	if err != nil {
		return "", err
	}
	data.Connections = append(data.Connections, connection)
	if err := a.Vault.Save(userID, password, data); err != nil {
		return "", err
	}
	return connection.ID, nil
}

func (a *ConnectionAPI) Test(ctx context.Context, userID, password string, groups []string, connection model.Connection, admin bool) error {
	if err := validateConnection(connection); err != nil {
		return err
	}
	if connection.Organization != "" {
		if _, err := a.organization(groups, connection.Organization); err != nil {
			return err
		}
	}
	if connection.ID != "" {
		if orgID, localID, ok := splitOrganizationID(connection.ID); ok {
			org, err := a.organization(groups, orgID)
			if err != nil {
				return err
			}
			existing, exists := org.Connections[localID]
			if !exists {
				return ErrConnectionNotFound
			}
			connection = mergeSecrets(connection, existing.Connection)
		} else if !(admin && connection.AccessKey != "" && connection.SecretKey != "") {
			data, _, err := a.Vault.Load(userID, password)
			if err != nil {
				return err
			}
			for _, existing := range data.Connections {
				if existing.ID == connection.ID {
					connection = mergeSecrets(connection, existing)
					break
				}
			}
		}
	}
	return a.Tester.TestConnection(ctx, connection)
}

func (a *ConnectionAPI) Update(ctx context.Context, userID, password string, groups []string, update model.Connection) error {
	if err := validateConnection(update); err != nil {
		return err
	}
	if update.ID == "" {
		return errors.New("connection id is required")
	}
	oldOrgID, localID, oldIsOrg := splitOrganizationID(update.ID)
	var oldOrg *model.Organization
	var existing model.Connection
	if oldIsOrg {
		org, err := a.organization(groups, oldOrgID)
		if err != nil {
			return err
		}
		current, ok := org.Connections[localID]
		if !ok {
			return ErrConnectionNotFound
		}
		existing, oldOrg = current.Connection, &org
	} else {
		data, _, err := a.Vault.Load(userID, password)
		if err != nil {
			return err
		}
		found := false
		for _, current := range data.Connections {
			if current.ID == update.ID {
				existing, found = current, true
				break
			}
		}
		if !found {
			return ErrConnectionNotFound
		}
		localID = update.ID
	}
	update = mergeSecrets(update, existing)
	if err := a.Tester.TestConnection(ctx, update); err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}
	if update.Organization != "" {
		org, err := a.organization(groups, update.Organization)
		if err != nil {
			return err
		}
		if org.Connections == nil {
			org.Connections = map[string]model.OrganizationConnection{}
		}
		update.ID, update.Organization = localID, ""
		org.Connections[localID] = model.OrganizationConnection{Connection: update}
		if err := a.saveOrganization(org); err != nil {
			return err
		}
		if oldOrg != nil && oldOrg.ID != org.ID {
			delete(oldOrg.Connections, localID)
			return a.saveOrganization(*oldOrg)
		}
		if oldOrg == nil {
			return a.removePersonal(userID, password, localID)
		}
		return nil
	}
	update.ID, update.Organization = localID, ""
	data, _, err := a.Vault.Load(userID, password)
	if err != nil {
		return err
	}
	if oldOrg != nil {
		delete(oldOrg.Connections, localID)
		if err := a.saveOrganization(*oldOrg); err != nil {
			return err
		}
	}
	found := false
	for i := range data.Connections {
		if data.Connections[i].ID == localID {
			data.Connections[i], found = update, true
			break
		}
	}
	if !found {
		data.Connections = append(data.Connections, update)
	}
	return a.Vault.Save(userID, password, data)
}

func (a *ConnectionAPI) Delete(userID, password string, groups []string, id string) error {
	if orgID, localID, ok := splitOrganizationID(id); ok {
		org, err := a.organization(groups, orgID)
		if err != nil {
			return err
		}
		if _, exists := org.Connections[localID]; !exists {
			return ErrConnectionNotFound
		}
		delete(org.Connections, localID)
		return a.saveOrganization(org)
	}
	return a.removePersonal(userID, password, id)
}

func (a *ConnectionAPI) removePersonal(userID, password, id string) error {
	data, _, err := a.Vault.Load(userID, password)
	if err != nil {
		return err
	}
	for i, connection := range data.Connections {
		if connection.ID == id {
			data.Connections = append(data.Connections[:i], data.Connections[i+1:]...)
			return a.Vault.Save(userID, password, data)
		}
	}
	return ErrConnectionNotFound
}
func (a *ConnectionAPI) organization(groups []string, id string) (model.Organization, error) {
	if a.ResolveOrganization == nil {
		return model.Organization{}, ErrOrganizationRepositoryNotConfigured
	}
	return a.ResolveOrganization(groups, id)
}
func (a *ConnectionAPI) saveOrganization(org model.Organization) error {
	if a.Organizations == nil {
		return ErrOrganizationRepositoryNotConfigured
	}
	return a.Organizations.Save(org)
}
func mergeSecrets(next, current model.Connection) model.Connection {
	if next.AccessKey == "" {
		next.AccessKey = current.AccessKey
	}
	if next.SecretKey == "" {
		next.SecretKey = current.SecretKey
	}
	return next
}
func splitOrganizationID(id string) (string, string, bool) {
	org, local, ok := strings.Cut(id, ":")
	return org, local, ok && org != "" && local != ""
}

var (
	ErrOrganizationConnection              = errors.New("organization connection is not accessible")
	ErrOrganizationNotFound                = errors.New("organization not found")
	ErrOrganizationMembership              = errors.New("organization membership required")
	ErrOrganizationRepositoryNotConfigured = errors.New("organization repository is not configured")
	ErrConnectionNotFound                  = errors.New("connection not found")
)

func validateConnection(connection model.Connection) error {
	if connection.Name == "" || connection.Bucket == "" {
		return errors.New("name and bucket are required")
	}
	if err := model.ValidateConnectionGroupPath(connection.GroupPath); err != nil {
		return err
	}
	return nil
}
