package idempotency_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"example.com/productcatalog/internal/domain"
	"example.com/productcatalog/internal/idempotency"
)

func TestTTLAndFailure(t *testing.T) {
	cache := idempotency.New[int](time.Minute, 1)
	fingerprint := sha256.Sum256([]byte("body"))
	calls := 0
	fn := func() (int, error) { calls++; return calls, nil }
	for i := 0; i < 2; i++ {
		value, err := cache.Do(context.Background(), "key", fingerprint, fn)
		if err != nil || value != 1 {
			t.Fatalf("cache: value=%d err=%v", value, err)
		}
	}
	if _, err := cache.Do(context.Background(), "other", fingerprint, fn); domain.KindOf(err) != domain.Overloaded {
		t.Fatalf("want capacity error, got %v", err)
	}
	expiring := idempotency.New[int](5*time.Millisecond, 1)
	if _, err := expiring.Do(context.Background(), "key", fingerprint, fn); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	value, err := expiring.Do(context.Background(), "key", fingerprint, fn)
	if err != nil || value != 3 {
		t.Fatalf("expiry: value=%d err=%v", value, err)
	}
	failureCache := idempotency.New[int](time.Minute, 1)
	_, err = failureCache.Do(context.Background(), "key", fingerprint, func() (int, error) { return 0, errors.New("transient") })
	if err == nil {
		t.Fatal("expected failure")
	}
	if _, err := failureCache.Do(context.Background(), "key", fingerprint, fn); err != nil {
		t.Fatalf("failure was cached: %v", err)
	}
}

func TestWaiterCancellation(t *testing.T) {
	cache := idempotency.New[int](time.Minute, 10)
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var calls atomic.Int32
	fingerprint := sha256.Sum256(nil)
	go func() {
		_, err := cache.Do(context.Background(), "key", fingerprint, func() (int, error) {
			calls.Add(1)
			close(started)
			<-release
			return 1, nil
		})
		finished <- err
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := cache.Do(ctx, "key", fingerprint, func() (int, error) { calls.Add(1); return 2, nil })
	close(release)
	if ownerErr := <-finished; ownerErr != nil {
		t.Fatal(ownerErr)
	}
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}
