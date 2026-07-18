package auth

import "testing"

func TestChallengeIsDeterministic(t *testing.T) {
	v := "test-verifier"
	if Challenge(v) != Challenge(v) {
		t.Fatal("challenge is not deterministic")
	}
	if Challenge(v) == Challenge("other") {
		t.Fatal("different verifiers share a challenge")
	}
}

func TestAuthorizationURL(t *testing.T) {
	u := AuthorizationURL("https://login.example/authorize", "client", "http://127.0.0.1/callback", "state", "challenge", "openid offline_access")
	for _, want := range []string{"client_id=client", "state=state", "code_challenge=challenge", "code_challenge_method=S256", "scope=openid"} {
		if !contains(u, want) {
			t.Fatalf("URL missing %q: %s", want, u)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
