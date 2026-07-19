package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"m365-native/internal/outbound"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type TokenSet struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresIn    int       `json:"expires_in,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	Email        string    `json:"email,omitempty"`
	DisplayName  string    `json:"display_name,omitempty"`
	HomeOID      string    `json:"home_oid,omitempty"`
	TenantID     string    `json:"tenant_id,omitempty"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

func (t TokenSet) Valid() bool {
	return t.AccessToken != "" && time.Now().Before(t.ExpiresAt.Add(-30*time.Second))
}

func ExchangeCode(code, verifier, redirect string) (TokenSet, error) {
	form := url.Values{}
	form.Set("client_id", ClientID())
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirect)
	form.Set("code_verifier", verifier)
	form.Set("scope", Scope())
	return requestToken(form)
}

func Refresh(refreshToken string) (TokenSet, error) {
	form := url.Values{}
	form.Set("client_id", ClientID())
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("scope", Scope())
	return requestToken(form)
}

func requestToken(form url.Values) (TokenSet, error) {
	req, err := http.NewRequest(http.MethodPost, TokenEndpoint(), strings.NewReader(form.Encode()))
	if err != nil {
		return TokenSet{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := outbound.HTTPClient().Do(req)
	if err != nil {
		return TokenSet{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return TokenSet{}, err
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return TokenSet{}, fmt.Errorf("decode token response: %w", err)
	}
	if tr.Error != "" {
		return TokenSet{}, fmt.Errorf("token endpoint HTTP %d: %s: %s", resp.StatusCode, tr.Error, tr.ErrorDesc)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return TokenSet{}, fmt.Errorf("token endpoint HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if tr.AccessToken == "" {
		return TokenSet{}, fmt.Errorf("token endpoint HTTP %d: empty access token: %s", resp.StatusCode, string(body))
	}
	set := TokenSet{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		IDToken:      tr.IDToken,
		TokenType:    tr.TokenType,
		Scope:        tr.Scope,
		ExpiresIn:    tr.ExpiresIn,
		ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
	}
	if claims, err := decodeJWTClaims(tr.AccessToken); err == nil {
		set.Email = firstNonEmpty(claims["unique_name"], claims["upn"], claims["preferred_username"], claims["email"])
		set.DisplayName = firstNonEmpty(claims["name"], set.Email)
		set.HomeOID = firstNonEmpty(claims["oid"], claims["sub"])
		set.TenantID = firstNonEmpty(claims["tid"], claims["tenant_id"])
	}
	if tr.IDToken != "" {
		if claims, err := decodeJWTClaims(tr.IDToken); err == nil {
			if set.Email == "" {
				set.Email = firstNonEmpty(claims["preferred_username"], claims["email"], claims["upn"])
				set.DisplayName = firstNonEmpty(claims["name"], set.Email)
				set.HomeOID = firstNonEmpty(claims["oid"], claims["sub"], set.HomeOID)
			}
			set.TenantID = firstNonEmpty(set.TenantID, claims["tid"], claims["tenant_id"])
		}
	}
	return set, nil
}

func decodeJWTClaims(token string) (map[string]string, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid jwt")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, v := range m {
		switch t := v.(type) {
		case string:
			out[k] = t
		}
	}
	return out, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
