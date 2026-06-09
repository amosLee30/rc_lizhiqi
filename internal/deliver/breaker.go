package deliver

import (
	"sync"
	"time"
)

// breaker is a simple per-supplier circuit breaker: after N consecutive
// failures it opens for a cooldown, isolating a sick supplier so it can't
// starve delivery to healthy ones.
type breaker struct {
	mu        sync.Mutex
	threshold int
	cooldown  time.Duration
	now       func() time.Time
	fails     map[string]int
	openUntil map[string]time.Time
}

func newBreaker(threshold int, cooldown time.Duration) *breaker {
	return &breaker{
		threshold: threshold,
		cooldown:  cooldown,
		now:       time.Now,
		fails:     map[string]int{},
		openUntil: map[string]time.Time{},
	}
}

// Open reports whether the breaker for supplier is currently open.
func (b *breaker) Open(supplier string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	until, ok := b.openUntil[supplier]
	return ok && b.now().Before(until)
}

// Success resets the failure count for a supplier.
func (b *breaker) Success(supplier string) {
	b.mu.Lock()
	delete(b.fails, supplier)
	delete(b.openUntil, supplier)
	b.mu.Unlock()
}

// Failure records a failure and opens the breaker once the threshold is hit.
func (b *breaker) Failure(supplier string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fails[supplier]++
	if b.fails[supplier] >= b.threshold {
		b.openUntil[supplier] = b.now().Add(b.cooldown)
	}
}
