package adapter

import (
	"encoding/json"
	"testing"
)

func TestBaseValidateRequiredFields(t *testing.T) {
	a := NewBearerAdapter("crm", "http://x", "POST", []string{"contactId"})
	if err := a.Validate(map[string]any{}); err == nil {
		t.Fatal("expected validation error for missing contactId")
	}
	if err := a.Validate(map[string]any{"contactId": "42"}); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestBearerBuildRequestInjectsToken(t *testing.T) {
	a := NewBearerAdapter("crm", "http://x/contact", "POST", nil)
	r, err := a.BuildRequest(map[string]any{"contactId": "42"}, "tok123")
	if err != nil {
		t.Fatal(err)
	}
	if r.Headers["Authorization"] != "Bearer tok123" {
		t.Fatalf("missing bearer token: %v", r.Headers)
	}
	var body map[string]any
	if err := json.Unmarshal(r.Body, &body); err != nil || body["contactId"] != "42" {
		t.Fatalf("unexpected body: %s", r.Body)
	}
}

func TestHMACBuildRequestSignsDeterministically(t *testing.T) {
	old := nowUnix
	nowUnix = func() int64 { return 1700000000 }
	defer func() { nowUnix = old }()

	a := NewHMACAdapter("pay-hmac", "http://x", "POST", []string{"orderId"})
	r, err := a.BuildRequest(map[string]any{"orderId": "A1"}, "sek")
	if err != nil {
		t.Fatal(err)
	}
	if r.Headers["X-Timestamp"] != "1700000000" || r.Headers["X-Signature"] == "" {
		t.Fatalf("expected signed headers, got %v", r.Headers)
	}
	// Same inputs => same signature (test vector style determinism).
	r2, _ := a.BuildRequest(map[string]any{"orderId": "A1"}, "sek")
	if r.Headers["X-Signature"] != r2.Headers["X-Signature"] {
		t.Fatal("signature not deterministic for identical inputs")
	}
}

func TestHMACRequiresSecret(t *testing.T) {
	a := NewHMACAdapter("pay-hmac", "http://x", "POST", nil)
	if _, err := a.BuildRequest(map[string]any{"orderId": "A1"}, ""); err == nil {
		t.Fatal("expected error when signing secret missing")
	}
}
