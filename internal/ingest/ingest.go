// Package ingest implements the accept path: idempotency, fail-fast validation
// via the supplier adapter, and persist-before-ack. It returns the interaction
// tracking ID (= notification id).
package ingest

import (
	"encoding/json"
	"errors"
	"fmt"

	"rc_lizhiqi/internal/adapter"
	"rc_lizhiqi/internal/config"
	"rc_lizhiqi/internal/id"
	"rc_lizhiqi/internal/metrics"
	"rc_lizhiqi/internal/model"
	"rc_lizhiqi/internal/store"
)

// ErrUnknownType is returned for a notification type with no registered adapter.
var ErrUnknownType = errors.New("unknown notification type")

// ErrValidation wraps a supplier-specific required-param failure.
type ErrValidation struct{ Err error }

func (e ErrValidation) Error() string { return "validation failed: " + e.Err.Error() }
func (e ErrValidation) Unwrap() error { return e.Err }

// Request is a submission from a business system.
type Request struct {
	IdempotencyKey string         `json:"idempotency_key"`
	SourceSystem   string         `json:"source_system"`
	Type           string         `json:"type"`
	Params         map[string]any `json:"params"`
}

// Result is returned to the caller; TrackingID is the stable interaction handle.
type Result struct {
	TrackingID string       `json:"tracking_id"`
	Status     model.Coarse `json:"status"`
	Duplicate  bool         `json:"duplicate"`
}

// Service is the accept service.
type Service struct {
	store     *store.Store
	registry  *adapter.Registry
	suppliers map[string]config.SupplierConfig
}

// New builds an accept service.
func New(s *store.Store, r *adapter.Registry, suppliers map[string]config.SupplierConfig) *Service {
	return &Service{store: s, registry: r, suppliers: suppliers}
}

// Accept validates and durably records a submission, returning a tracking ID.
func (s *Service) Accept(req Request) (*Result, error) {
	if req.IdempotencyKey == "" || req.SourceSystem == "" || req.Type == "" {
		return nil, fmt.Errorf("idempotency_key, source_system and type are required")
	}
	ad, ok := s.registry.Get(req.Type)
	if !ok {
		return nil, ErrUnknownType
	}
	// fail-fast required-param validation via the adapter
	if err := ad.Validate(req.Params); err != nil {
		return nil, ErrValidation{Err: err}
	}

	params, err := json.Marshal(req.Params)
	if err != nil {
		return nil, err
	}
	maxAttempts := 5
	if c, ok := s.suppliers[req.Type]; ok && c.MaxAttempts > 0 {
		maxAttempts = c.MaxAttempts
	}

	n := &model.Notification{
		ID:             id.New(),
		IdempotencyKey: req.IdempotencyKey,
		SourceSystem:   req.SourceSystem,
		Type:           req.Type,
		Params:         string(params),
		MaxAttempts:    maxAttempts,
	}
	trackingID, created, err := s.store.Accept(n)
	if err != nil {
		return nil, err
	}
	if created {
		metrics.Inc("accepted")
	} else {
		metrics.Inc("accepted_duplicate")
	}
	return &Result{TrackingID: trackingID, Status: model.CoarseAccepted, Duplicate: !created}, nil
}
