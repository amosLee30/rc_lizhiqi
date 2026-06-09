package ingest

import (
	"errors"
	"path/filepath"
	"testing"

	"rc_lizhiqi/internal/adapter"
	"rc_lizhiqi/internal/config"
	"rc_lizhiqi/internal/model"
	"rc_lizhiqi/internal/store"
)

func newSvc(t *testing.T) *Service {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	reg := adapter.NewRegistry()
	reg.Register(adapter.NewBearerAdapter("crm", "http://x", "POST", []string{"contactId"}))
	return New(s, reg, map[string]config.SupplierConfig{"crm": {Type: "crm", MaxAttempts: 4}})
}

func TestAcceptUnknownTypeRejected(t *testing.T) {
	svc := newSvc(t)
	_, err := svc.Accept(Request{IdempotencyKey: "k1", SourceSystem: "s", Type: "ghost", Params: map[string]any{}})
	if !errors.Is(err, ErrUnknownType) {
		t.Fatalf("expected ErrUnknownType, got %v", err)
	}
}

func TestAcceptValidationFailFast(t *testing.T) {
	svc := newSvc(t)
	_, err := svc.Accept(Request{IdempotencyKey: "k1", SourceSystem: "s", Type: "crm", Params: map[string]any{}})
	if !isValidationErr(err) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestAcceptReturnsTrackingIDAndIsIdempotent(t *testing.T) {
	svc := newSvc(t)
	req := Request{IdempotencyKey: "order-123", SourceSystem: "billing", Type: "crm", Params: map[string]any{"contactId": "42"}}
	r1, err := svc.Accept(req)
	if err != nil {
		t.Fatal(err)
	}
	if r1.TrackingID == "" || r1.Status != model.CoarseAccepted || r1.Duplicate {
		t.Fatalf("unexpected first result: %+v", r1)
	}
	r2, err := svc.Accept(req)
	if err != nil {
		t.Fatal(err)
	}
	if !r2.Duplicate || r2.TrackingID != r1.TrackingID {
		t.Fatalf("idempotent resubmit must return same tracking id: %+v vs %+v", r2, r1)
	}
}

func isValidationErr(err error) bool {
	var v ErrValidation
	return errors.As(err, &v)
}
