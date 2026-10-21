package repository_test

import (
	"testing"

	"mariner/internal/repository"
	"mariner/internal/vault"
)

func TestTableRepositoriesImplementContracts(t *testing.T) {
	var _ repository.VaultRepository = (*vault.VaultTableRepository)(nil)
	var _ repository.OrganizationRepository = (*vault.OrganizationTableRepository)(nil)
	var _ repository.AuditEventRepository = (*vault.AuditEventTableRepository)(nil)
	var _ repository.UserPreferenceRepository = (*vault.UserPreferenceTableRepository)(nil)
}
