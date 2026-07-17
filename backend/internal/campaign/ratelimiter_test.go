package campaign

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRateLimiter_AllowsUpToMaxThenBlocks(t *testing.T) {
	rl := newRateLimiter(3, time.Hour)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := rl.wait(ctx); err != nil {
			t.Fatalf("reservation %d should succeed, got %v", i+1, err)
		}
	}

	// The 4th reservation must block until the window slides; with a 50ms
	// deadline against a 1h window it can only end in DeadlineExceeded.
	ctx2, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := rl.wait(ctx2); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected 4th reservation to block until deadline, got %v", err)
	}
}

func TestRateLimiter_WindowSlides(t *testing.T) {
	const window = 60 * time.Millisecond
	rl := newRateLimiter(2, window)
	ctx := context.Background()

	if err := rl.wait(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rl.wait(ctx); err != nil {
		t.Fatal(err)
	}

	// The 3rd reservation should wait roughly one window for the first to expire.
	start := time.Now()
	if err := rl.wait(ctx); err != nil {
		t.Fatalf("3rd reservation should eventually succeed, got %v", err)
	}
	if elapsed := time.Since(start); elapsed < window/2 {
		t.Errorf("expected the limiter to wait for the window to slide, only waited %v", elapsed)
	}
}
