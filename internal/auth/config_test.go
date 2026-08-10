package auth

import (
	"strings"
	"testing"
)

func TestBrowserPKCEDefaultsRemainMatched(t *testing.T) {
	for _, key := range []string{
		"M365_CLIENT_ID",
		"M365_AUTHORITY",
		"M365_REDIRECT_URI",
	} {
		t.Setenv(key, "")
	}

	if got, want := ClientID(), "c0ab8ce9-e9a0-42e7-b064-33d422df41f1"; got != want {
		t.Fatalf("ClientID() = %q, want %q", got, want)
	}
	if got, want := Authority(), "https://login.microsoftonline.com/common"; got != want {
		t.Fatalf("Authority() = %q, want %q", got, want)
	}
	if got, want := RedirectURI(), "https://login.microsoftonline.com/common/oauth2/nativeclient"; got != want {
		t.Fatalf("RedirectURI() = %q, want %q", got, want)
	}
}

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
	t.Setenv("M365_AUTHORITY", custom)
	if got := Authority(); got != custom {
		t.Fatalf("Authority() = %q", got)
	}
}

func TestBrowserAndDeviceConfigurationAreIsolated(t *testing.T) {
	t.Setenv("M365_BROWSER_CLIENT_ID", "browser-client")
	t.Setenv("M365_BROWSER_AUTHORITY", "https://login.microsoftonline.com/organizations")
	t.Setenv("M365_BROWSER_SCOPE", "browser-scope")
	t.Setenv("M365_BROWSER_REDIRECT_URI", "http://127.0.0.1:4141/api/auth/callback")
	t.Setenv("M365_DEVICE_CLIENT_ID", "device-client")
	t.Setenv("M365_DEVICE_AUTHORITY", "https://login.microsoftonline.com/consumers")
	t.Setenv("M365_DEVICE_SCOPE", "device-scope")

	if ClientID() != "browser-client" || Authority() != "https://login.microsoftonline.com/organizations" || Scope() != "browser-scope" {
		t.Fatal("browser OAuth configuration was not isolated")
	}
	if DeviceClientID() != "device-client" || DeviceAuthority() != "https://login.microsoftonline.com/consumers" || DeviceScope() != "device-scope" {
		t.Fatal("device-code configuration was not isolated")
	}
}

func TestLegacyOAuthConfigurationRemainsCompatible(t *testing.T) {
	t.Setenv("M365_BROWSER_CLIENT_ID", "")
	t.Setenv("M365_BROWSER_AUTHORITY", "")
	t.Setenv("M365_BROWSER_SCOPE", "")
	t.Setenv("M365_BROWSER_REDIRECT_URI", "")
	t.Setenv("M365_DEVICE_CLIENT_ID", "")
	t.Setenv("M365_DEVICE_AUTHORITY", "")
	t.Setenv("M365_DEVICE_SCOPE", "")
	t.Setenv("M365_CLIENT_ID", "legacy-client")
	t.Setenv("M365_AUTHORITY", "https://login.microsoftonline.com/common")
	t.Setenv("M365_SCOPE", "legacy-scope")
	t.Setenv("M365_REDIRECT_URI", "https://login.microsoftonline.com/common/oauth2/nativeclient")

	if ClientID() != "legacy-client" || DeviceClientID() != "legacy-client" {
		t.Fatal("legacy client ID was not inherited")
	}
	if Scope() != "legacy-scope" || DeviceScope() != "legacy-scope" {
		t.Fatal("legacy scope was not inherited")
	}
}
