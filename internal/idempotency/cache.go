// Package idempotency coalesces concurrent requests and retains successful responses.
package idempotency

import (
	"context"
	"sync"
	"time"

	"example.com/productcatalog/internal/domain"
)

type entry[T any] struct {
	fingerprint [32]byte
	done        chan struct{}
	value       T
	err         error
	expires     time.Time
}

type Cache[T any] struct {
	mu       sync.Mutex
	entries  map[string]*entry[T]
	ttl      time.Duration
	capacity int
}

func New[T any](ttl time.Duration, capacity int) *Cache[T] {
	return &Cache[T]{entries: make(map[string]*entry[T]), ttl: ttl, capacity: capacity}
}

// Values must be immutable after fn returns. Errors are shared with waiters but not cached.
func (c *Cache[T]) Do(ctx context.Context, key string, fingerprint [32]byte, fn func() (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	c.mu.Lock()
	now := time.Now()
	if e, ok := c.entries[key]; ok {
		if e.expires.IsZero() || now.Before(e.expires) {
			c.mu.Unlock()
			if e.fingerprint != fingerprint {
				return zero, domain.NewError(domain.Conflict, "idempotency key reused with a different request")
			}
			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-e.done:
				return e.value, e.err
			}
		}
		delete(c.entries, key)
	}
	if len(c.entries) >= c.capacity {
		for k, e := range c.entries {
			if !e.expires.IsZero() && !now.Before(e.expires) {
				delete(c.entries, k)
			}
		}
	}
	if len(c.entries) >= c.capacity {
		c.mu.Unlock()
		return zero, domain.NewError(domain.Overloaded, "idempotency cache is full")
	}
	e := &entry[T]{fingerprint: fingerprint, done: make(chan struct{})}
	c.entries[key] = e
	c.mu.Unlock()
	value, err := fn()
	c.mu.Lock()
	e.value, e.err = value, err
	if err != nil {
		delete(c.entries, key)
	} else {
		e.expires = time.Now().Add(c.ttl)
	}
	close(e.done)
	c.mu.Unlock()
	return value, err
}
