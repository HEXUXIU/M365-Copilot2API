package chathub

import (
	"errors"
	"reflect"
	"testing"
)

func TestCheckMeteringErrorRequiresExplicitDenialAndRetainsPayload(t *testing.T) {
	missingAccess := []any{map[string]any{
		"meterError": "ImageGenInsufficientTokensThrottled",
	}}
	if err := checkMeteringError(missingAccess); err != nil {
		t.Fatalf("missing hasAccess must remain unknown, got %v", err)
	}

	denied := []any{map[string]any{
		"meterError": "ImageGenInsufficientTokensThrottled",
		"hasAccess":  false,
	}}
	err := checkMeteringError(denied)
	if !errors.Is(err, ErrImageLimit) {
		t.Fatalf("explicit image denial error=%v, want ErrImageLimit", err)
	}
	var meteringErr *MeteringError
	if !errors.As(err, &meteringErr) {
		t.Fatalf("error type=%T, want *MeteringError", err)
	}
	if !reflect.DeepEqual(meteringErr.Information, denied) {
		t.Fatalf("metering payload=%#v, want %#v", meteringErr.Information, denied)
	}
}
