package auth

import (
	"os"
	"strings"
	"testing"
)

func TestDefaultAuthorityIsMultitenant(t *testing.T) {
	t.Setenv("M365_AUTHORITY", "")
	if got := Authority(); got != "https://login.microsoftonline.com/common" {
		t.Fatalf("Authority() = %q", got)
	}
	if !strings.Contains(AuthorizeEndpoint(), "/common/") {
		t.Fatal("default authorize endpoint must be multitenant")
	}
}

func TestAuthorityOverride(t *testing.T) {
	const custom = "https://login.microsoftonline.com/organizations"
	if err := os.Setenv("M365_AUTHORITY", custom); err != nil {
		t.Fatal(err)
	}
	defer os.Unsetenv("M365_AUTHORITY")
	if got := Authority(); got != custom {
		t.Fatalf("Authority() = %q", got)
	}
}
