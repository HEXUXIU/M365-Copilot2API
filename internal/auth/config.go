package auth

import "os"

// Office web Copilot first-party client (verified working with ChatHub via browser PKCE).
// The default authority is multi-tenant so any supported Microsoft account can sign in.
// Device-code/FOCI client can still be forced via M365_CLIENT_ID.
const DefaultClientID = "efcea265-005c-4f0a-97c2-b3ab369c8484"
const DefaultTenantID = "common"
const FOCIClientID = "d3590ed6-52b3-4102-aeff-aad2292ab01c"
const DefaultAuthority = "https://login.microsoftonline.com/common"

// Loopback callback lets the browser return to the running gateway automatically.
// Set M365_REDIRECT_URI back to the nativeclient URI when using a tenant/client
// registration that does not allow loopback redirects.
const DefaultRedirectURI = "http://127.0.0.1:4141/api/auth/callback"
const DefaultScope = "openid profile offline_access https://substrate.office.com/sydney/M365Chat.Read https://substrate.office.com/sydney/sydney.readwrite"

func ClientID() string {
	if v := os.Getenv("M365_CLIENT_ID"); v != "" {
		return v
	}
	return DefaultClientID
}

func Authority() string {
	if v := os.Getenv("M365_AUTHORITY"); v != "" {
		return v
	}
	return DefaultAuthority
}

func RedirectURI() string {
	if v := os.Getenv("M365_REDIRECT_URI"); v != "" {
		return v
	}
	return DefaultRedirectURI
}

func Scope() string {
	if v := os.Getenv("M365_SCOPE"); v != "" {
		return v
	}
	return DefaultScope
}

func AuthorizeEndpoint() string {
	if v := os.Getenv("M365_AUTHORIZE_ENDPOINT"); v != "" {
		return v
	}
	return Authority() + "/oauth2/v2.0/authorize"
}

func TokenEndpoint() string {
	if v := os.Getenv("M365_TOKEN_ENDPOINT"); v != "" {
		return v
	}
	return Authority() + "/oauth2/v2.0/token"
}

func DeviceCodeEndpoint() string {
	if v := os.Getenv("M365_DEVICE_ENDPOINT"); v != "" {
		return v
	}
	return Authority() + "/oauth2/v2.0/devicecode"
}
