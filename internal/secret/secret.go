// Package secret resolves credential references at delivery time (late binding).
//
// v1 backends: a local JSON file (stand-in for sops-encrypted config) with an
// environment-variable fallback. The Resolver abstraction lets us swap in
// Vault/KMS later without touching callers.
package secret

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Resolver resolves a secret_ref to its real value.
type Resolver interface {
	Resolve(ref string) (string, error)
}

// fileResolver reads refs from an in-memory map loaded from a JSON file,
// falling back to environment variables for "env:NAME" refs.
type fileResolver struct {
	values map[string]string
}

// NewFileResolver loads a JSON object of {ref: value}. Missing file is allowed
// (empty map) so env-only setups still work.
func NewFileResolver(path string) (Resolver, error) {
	values := map[string]string{}
	if path != "" {
		b, err := os.ReadFile(path)
		if err == nil {
			if err := json.Unmarshal(b, &values); err != nil {
				return nil, fmt.Errorf("parse secrets file %s: %w", path, err)
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read secrets file %s: %w", path, err)
		}
	}
	return &fileResolver{values: values}, nil
}

func (r *fileResolver) Resolve(ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	if name, ok := strings.CutPrefix(ref, "env:"); ok {
		v, present := os.LookupEnv(name)
		if !present {
			return "", fmt.Errorf("secret env %q not set", name)
		}
		return v, nil
	}
	if v, ok := r.values[ref]; ok {
		return v, nil
	}
	return "", fmt.Errorf("secret ref %q not found", ref)
}
