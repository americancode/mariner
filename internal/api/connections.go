package api

import (
	"context"
	"errors"
	"fmt"

	"mariner/internal/model"
	"mariner/internal/repository"
)

type ConnectionTester interface {
	TestConnection(ctx context.Context, connection model.Connection) error
}

type ConnectionAPI struct {
	Vault                    repository.VaultRepository
	Tester                   ConnectionTester
	NewID                    func() string
	IsOrganizationConnection func(id string) bool
}

func NewConnectionAPI(vault repository.VaultRepository, tester ConnectionTester, newID func() string, isOrganizationConnection func(string) bool) *ConnectionAPI {
	return &ConnectionAPI{Vault: vault, Tester: tester, NewID: newID, IsOrganizationConnection: isOrganizationConnection}
}

func (a *ConnectionAPI) List(userID, password string) ([]model.Connection, error) {
	data, _, err := a.Vault.Load(userID, password)
	if err != nil {
		return nil, err
	}
	return append([]model.Connection(nil), data.Connections...), nil
}

func (a *ConnectionAPI) Create(ctx context.Context, userID, password string, connection model.Connection) (string, error) {
	if err := validateConnection(connection); err != nil {
		return "", err
	}
	if err := a.Tester.TestConnection(ctx, connection); err != nil {
		return "", fmt.Errorf("connection test failed: %w", err)
	}
	data, _, err := a.Vault.Load(userID, password)
	if err != nil {
		return "", err
	}
	connection.ID = a.NewID()
	data.Connections = append(data.Connections, connection)
	if err := a.Vault.Save(userID, password, data); err != nil {
		return "", err
	}
	return connection.ID, nil
}

func (a *ConnectionAPI) Test(ctx context.Context, userID, password string, connection model.Connection, admin bool) error {
	if err := validateConnection(connection); err != nil {
		return err
	}
	var data model.Data
	if !(admin && connection.AccessKey != "" && connection.SecretKey != "") {
		loaded, _, err := a.Vault.Load(userID, password)
		if err != nil {
			return err
		}
		data = loaded
	}
	if connection.ID != "" {
		for _, existing := range data.Connections {
			if existing.ID == connection.ID {
				if connection.AccessKey == "" {
					connection.AccessKey = existing.AccessKey
				}
				if connection.SecretKey == "" {
					connection.SecretKey = existing.SecretKey
				}
				break
			}
		}
	}
	return a.Tester.TestConnection(ctx, connection)
}

func (a *ConnectionAPI) Update(ctx context.Context, userID, password string, update model.Connection) error {
	if err := validateConnection(update); err != nil {
		return err
	}
	if update.ID == "" {
		return errors.New("connection id is required")
	}
	if a.IsOrganizationConnection != nil && a.IsOrganizationConnection(update.ID) {
		return ErrOrganizationConnection
	}
	data, _, err := a.Vault.Load(userID, password)
	if err != nil {
		return err
	}
	for i := range data.Connections {
		if data.Connections[i].ID != update.ID {
			continue
		}
		if update.AccessKey == "" {
			update.AccessKey = data.Connections[i].AccessKey
		}
		if update.SecretKey == "" {
			update.SecretKey = data.Connections[i].SecretKey
		}
		if err := a.Tester.TestConnection(ctx, update); err != nil {
			return fmt.Errorf("connection test failed: %w", err)
		}
		data.Connections[i] = update
		return a.Vault.Save(userID, password, data)
	}
	return ErrConnectionNotFound
}

func (a *ConnectionAPI) Delete(userID, password, id string) error {
	if a.IsOrganizationConnection != nil && a.IsOrganizationConnection(id) {
		return ErrOrganizationConnection
	}
	data, _, err := a.Vault.Load(userID, password)
	if err != nil {
		return err
	}
	for i, connection := range data.Connections {
		if connection.ID == id {
			data.Connections = append(data.Connections[:i], data.Connections[i+1:]...)
			break
		}
	}
	return a.Vault.Save(userID, password, data)
}

var (
	ErrOrganizationConnection = errors.New("organization connections cannot be edited or deleted")
	ErrConnectionNotFound     = errors.New("connection not found")
)

func validateConnection(connection model.Connection) error {
	if connection.Name == "" || connection.Bucket == "" {
		return errors.New("name and bucket are required")
	}
	return nil
}
