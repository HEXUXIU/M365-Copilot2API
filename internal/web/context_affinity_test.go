package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClassifyErrorRefreshRejection(t *testing.T) {
	cases := []string{
		"Refresh invalid_grant: AADSTS5000224: We are sorry, this resource is not available.",
		"token_expired: refresh token missing or expired",
	}
	for _, msg := range cases {
		if got := ClassifyError(errors.New(msg)); got != CategoryAuthExpired401 {
			t.Fatalf("ClassifyError(%q)=%v, want %v", msg, got, CategoryAuthExpired401)
		}
	}
	// A service-wide client configuration error (AADSTS7000215) is not a
	// per-account auth failure, so it must not cool accounts down.
	if got := ClassifyError(errors.New("invalid_client: AADSTS7000215: Invalid client secret provided.")); got == CategoryAuthExpired401 {
		t.Fatalf("global client error must not be classified as per-account auth: %v", got)
	}
	// A transient transport error must not be mistaken for an auth failure.
	if got := ClassifyError(errors.New("dial tcp: connection refused")); got == CategoryAuthExpired401 {
		t.Fatalf("transport error misclassified as auth failure: %v", got)
	}
}

func TestForgetAccountDropsStickyBindings(t *testing.T) {
	store := testAccountFiles(t)
	s := &Server{
		tokens:             store,
		accountPool:        newAccountHealth(),
		accountConcurrency: newAccountConcurrency(),
		contextAffinity:    newContextAffinity(),
	}
	body := &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "hello world"}}}
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	first, ok := s.contextAffinity.Resolve(s, r, body)
	if !ok || first == "" {
		t.Fatalf("expected an affinity assignment, got %q ok=%v", first, ok)
	}
	hash := contextHash(body.Messages)
	s.contextAffinity.mu.Lock()
	_, bound := s.contextAffinity.byHash[hash]
	before := len(s.contextAffinity.bindings)
	s.contextAffinity.mu.Unlock()
	if !bound || before == 0 {
		t.Fatalf("expected a binding for hash %s, bound=%v n=%d", hash, bound, before)
	}

	s.contextAffinity.ForgetAccount(first)

	s.contextAffinity.mu.Lock()
	_, stillBound := s.contextAffinity.byHash[hash]
	after := len(s.contextAffinity.bindings)
	s.contextAffinity.mu.Unlock()
	if stillBound || after != 0 {
		t.Fatalf("ForgetAccount left bindings behind: byHash=%v remaining=%d", stillBound, after)
	}
}
