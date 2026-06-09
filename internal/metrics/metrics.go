// Package metrics is a tiny in-memory counter registry for observability.
package metrics

import (
	"sort"
	"sync"
)

var (
	mu sync.Mutex
	c  = map[string]int64{}
)

// Inc increments a named counter.
func Inc(name string) { Add(name, 1) }

// Add adds delta to a named counter.
func Add(name string, delta int64) {
	mu.Lock()
	c[name] += delta
	mu.Unlock()
}

// Snapshot returns a copy of all counters, sorted by name.
func Snapshot() map[string]int64 {
	mu.Lock()
	defer mu.Unlock()
	out := make(map[string]int64, len(c))
	for k, v := range c {
		out[k] = v
	}
	return out
}

// Names returns the sorted counter names (handy for stable output).
func Names() []string {
	mu.Lock()
	defer mu.Unlock()
	ns := make([]string, 0, len(c))
	for k := range c {
		ns = append(ns, k)
	}
	sort.Strings(ns)
	return ns
}
