package httpapi

import (
	"testing"

	"mariner/internal/model"
)

func TestAppendGroupPathAddsAllAncestorsWithoutCrossJoining(t *testing.T) {
	groups := appendGroupPath([]string{"Existing"}, "Engineering/Production")
	if len(groups) != 3 || !containsString(groups, "Engineering") || !containsString(groups, "Engineering/Production") {
		t.Fatalf("groups = %#v, want existing group plus both ancestors", groups)
	}
}

func TestConnectionGroupPathsIncludesConnectionAncestors(t *testing.T) {
	paths := connectionGroupPaths(nil, []model.Connection{{GroupPath: "One/Two"}})
	if len(paths) != 2 || paths[0] != "One" || paths[1] != "One/Two" {
		t.Fatalf("paths = %#v, want implicit ancestors", paths)
	}
}

func TestGroupHasConnectionsRecognizesDescendants(t *testing.T) {
	if !groupHasConnections("One", []model.Connection{{GroupPath: "One/Two"}}) {
		t.Fatal("parent group did not detect descendant connection")
	}
	if groupHasConnections("Other", []model.Connection{{GroupPath: "One/Two"}}) {
		t.Fatal("unrelated group detected a connection")
	}
}

func TestPublicConnectionsPreservesOrganizationMetadata(t *testing.T) {
	connections := publicConnections(model.Data{Connections: []model.Connection{{
		ID: "org1:bucket", Name: "Bucket One", Bucket: "bucket-one",
		Organization: "org1", OrganizationCanManage: true, GroupPath: "Production",
	}}})
	if len(connections) != 1 {
		t.Fatalf("connections = %#v, want one connection", connections)
	}
	if connections[0].Organization != "org1" || !connections[0].OrganizationCanManage || connections[0].GroupPath != "Production" {
		t.Fatalf("connection metadata = %#v, organization metadata was not preserved", connections[0])
	}
}

func TestRenameGroupPathUpdatesNestedPaths(t *testing.T) {
	if got := renameGroupPath("Engineering/Production", "Engineering", "Platform"); got != "Platform/Production" {
		t.Fatalf("renamed path = %q, want Platform/Production", got)
	}
	if got := renameGroupPaths([]string{"Engineering", "Engineering/Production", "Other"}, "Engineering", "Platform"); len(got) != 3 || got[0] != "Platform" || got[1] != "Platform/Production" || got[2] != "Other" {
		t.Fatalf("renamed groups = %#v", got)
	}
}
