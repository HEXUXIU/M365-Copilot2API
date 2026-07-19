package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"m365-native/internal/outbound"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type DeviceCode struct {
	UserCode        string    `json:"user_code"`
	DeviceCode      string    `json:"device_code"`
	VerificationURI string    `json:"verification_uri"`
	Message         string    `json:"message"`
	ExpiresIn       int       `json:"expires_in"`
	Interval        int       `json:"interval"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type deviceCodeResponse struct {
	UserCode        string `json:"user_code"`
	DeviceCode      string `json:"device_code"`
	VerificationURI string `json:"verification_uri"`
	Message         string `json:"message"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Error           string `json:"error"`
	ErrorDesc       string `json:"error_description"`
}

func StartDeviceCode() (DeviceCode, error) {
	form := url.Values{}
	form.Set("client_id", ClientID())
	form.Set("scope", Scope())
	req, err := http.NewRequest(http.MethodPost, DeviceCodeEndpoint(), strings.NewReader(form.Encode()))
	if err != nil {
		return DeviceCode{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := outbound.HTTPClient().Do(req)
	if err != nil {
		return DeviceCode{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return DeviceCode{}, err
	}
	var dr deviceCodeResponse
	if err := json.Unmarshal(body, &dr); err != nil {
		return DeviceCode{}, err
	}
	if dr.Error != "" {
		return DeviceCode{}, fmt.Errorf("%s: %s", dr.Error, dr.ErrorDesc)
	}
	if dr.DeviceCode == "" || dr.UserCode == "" {
		return DeviceCode{}, fmt.Errorf("invalid device code response: %s", string(body))
	}
	interval := dr.Interval
	if interval <= 0 {
		interval = 5
	}
	return DeviceCode{
		UserCode:        dr.UserCode,
		DeviceCode:      dr.DeviceCode,
		VerificationURI: dr.VerificationURI,
		Message:         dr.Message,
		ExpiresIn:       dr.ExpiresIn,
		Interval:        interval,
		ExpiresAt:       time.Now().Add(time.Duration(dr.ExpiresIn) * time.Second),
	}, nil
}

func PollDeviceCode(deviceCode string) (TokenSet, bool, error) {
	form := url.Values{}
	form.Set("client_id", ClientID())
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	form.Set("device_code", deviceCode)
	req, err := http.NewRequest(http.MethodPost, TokenEndpoint(), strings.NewReader(form.Encode()))
	if err != nil {
		return TokenSet{}, false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := outbound.HTTPClient().Do(req)
	if err != nil {
		return TokenSet{}, false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return TokenSet{}, false, err
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return TokenSet{}, false, err
	}
	switch tr.Error {
	case "":
		// success path falls through
	case "authorization_pending", "slow_down":
		return TokenSet{}, false, nil
	case "expired_token", "authorization_declined", "bad_verification_code":
		return TokenSet{}, false, fmt.Errorf("%s: %s", tr.Error, tr.ErrorDesc)
	default:
		if tr.AccessToken == "" {
			return TokenSet{}, false, fmt.Errorf("%s: %s", tr.Error, tr.ErrorDesc)
		}
	}
	if tr.AccessToken == "" {
		return TokenSet{}, false, fmt.Errorf("empty access token: %s", string(body))
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
	return set, true, nil
}
