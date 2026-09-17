package httpapi

import "testing"

func TestHasGroupAcceptsOIDCPathAndShortGroupForms(t *testing.T) {
	if !hasGroup([]string{"/admins"}, []string{"admins"}) {
		t.Fatal("expected a slash-prefixed OIDC group to match")
	}
	if !hasGroup([]string{"admins"}, []string{"/admins"}) {
		t.Fatal("expected a short OIDC group to match a slash-prefixed requirement")
	}
}
