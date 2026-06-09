// Package adapter defines the per-supplier adaptation layer.
//
// One implementation class per supplier owns: required-param validation,
// request assembly + signing, and response handling. Common abilities are
// pulled into BaseAdapter. Transport/retry/lease are NOT here — they belong to
// the generic delivery worker.
package adapter

import (
	"fmt"
	"log/slog"
	"sync"
)

// Request is the wire request an adapter produces for the worker to send.
type Request struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
}

// SupplierAdapter is implemented once per supplier.
type SupplierAdapter interface {
	// Type is the supplier/notification type key.
	Type() string
	// Validate checks required params at accept time (fail-fast). MUST be pure.
	Validate(params map[string]any) error
	// BuildRequest assembles the request and applies auth/signing at delivery time.
	// secret is the resolved credential (may be empty for unauthenticated suppliers).
	BuildRequest(params map[string]any, secret string) (*Request, error)
	// HandleResponse lets the adapter inspect/log the response (no business-semantic
	// judgement of success — that stays HTTP-2xx based in the worker).
	HandleResponse(status int, body []byte)
}

// BaseAdapter provides common abilities for concrete adapters to embed.
type BaseAdapter struct {
	TypeName string
	Endpoint string
	Method   string
	Required []string
}

// Validate enforces presence of Required fields — the common case.
func (b BaseAdapter) Validate(params map[string]any) error {
	for _, f := range b.Required {
		v, ok := params[f]
		if !ok || v == nil || v == "" {
			return fmt.Errorf("missing required param %q", f)
		}
	}
	return nil
}

// HandleResponse logs the response by default; adapters may override.
func (b BaseAdapter) HandleResponse(status int, body []byte) {
	slog.Debug("supplier response", "type", b.TypeName, "status", status, "bytes", len(body))
}

// Registry maps supplier type -> adapter.
type Registry struct {
	mu sync.RWMutex
	m  map[string]SupplierAdapter
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{m: map[string]SupplierAdapter{}} }

// Register adds an adapter (last write wins).
func (r *Registry) Register(a SupplierAdapter) {
	r.mu.Lock()
	r.m[a.Type()] = a
	r.mu.Unlock()
}

// Get returns the adapter for a type and whether it exists.
func (r *Registry) Get(typ string) (SupplierAdapter, bool) {
	r.mu.RLock()
	a, ok := r.m[typ]
	r.mu.RUnlock()
	return a, ok
}
