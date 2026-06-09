// Package config loads app and supplier configuration.
package config

import (
	"encoding/json"
	"os"
)

// SupplierConfig holds connection + credential reference for one supplier.
// Request assembly/signing logic lives in code adapters, NOT here.
type SupplierConfig struct {
	Type        string `json:"type"`
	Endpoint    string `json:"endpoint"`
	Method      string `json:"method"`
	SecretRef   string `json:"secret_ref"`
	MaxAttempts int    `json:"max_attempts"`
	Version     int    `json:"version"`
}

// App is the top-level application configuration.
type App struct {
	Addr            string                    `json:"addr"`
	DBPath          string                    `json:"db_path"`
	SecretsFile     string                    `json:"secrets_file"`
	OpsToken        string                    `json:"ops_token"` // gates detail queries / admin
	Workers         int                       `json:"workers"`
	PollIntervalMS  int                       `json:"poll_interval_ms"`
	ClaimBatch      int                       `json:"claim_batch"`
	LeaseSeconds    int                       `json:"lease_seconds"`
	HTTPTimeoutMS   int                       `json:"http_timeout_ms"`
	BreakerFailures int                       `json:"breaker_failures"`
	BreakerCoolMS   int                       `json:"breaker_cooldown_ms"`
	Suppliers       map[string]SupplierConfig `json:"-"`
}

// Defaults returns a runnable default configuration.
func Defaults() App {
	return App{
		Addr:            ":8080",
		DBPath:          "notify.db",
		SecretsFile:     "secrets.json",
		OpsToken:        "ops-secret",
		Workers:         4,
		PollIntervalMS:  300,
		ClaimBatch:      20,
		LeaseSeconds:    30,
		HTTPTimeoutMS:   10000,
		BreakerFailures: 5,
		BreakerCoolMS:   10000,
		Suppliers:       map[string]SupplierConfig{},
	}
}

// LoadSuppliers reads a JSON array of SupplierConfig from path into a map keyed
// by type. A missing path yields an empty map (suppliers can be registered in code/tests).
func LoadSuppliers(path string) (map[string]SupplierConfig, error) {
	out := map[string]SupplierConfig{}
	if path == "" {
		return out, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	var list []SupplierConfig
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	for _, c := range list {
		if c.MaxAttempts == 0 {
			c.MaxAttempts = 5
		}
		out[c.Type] = c
	}
	return out, nil
}
