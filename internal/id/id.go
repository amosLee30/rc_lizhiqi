// Package id generates time-sortable unique IDs (ULID-ish) without external deps.
package id

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

var mu sync.Mutex

// New returns a lexicographically sortable, unique ID: <ms-hex>-<random-hex>.
func New() string {
	mu.Lock()
	defer mu.Unlock()
	ms := time.Now().UnixMilli()
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%012x-%s", ms, hex.EncodeToString(b))
}
