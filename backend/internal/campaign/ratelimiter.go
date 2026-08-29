package campaign

import (
	"context"
	"sync"
	"time"
)

// rateLimiter is a sliding-window limiter: at most max events are allowed in any
// rolling window. It is the per-number hourly governance cap — one
// limiter exists per WhatsApp session id, so two campaigns sharing a number can
// never jointly exceed the cap.
//
// wait reserves a slot (blocking until one frees up), recording the timestamp at
// reservation time so a burst of callers can't slip past the ceiling.
type rateLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	stamps []time.Time // reservation timestamps still inside the window, oldest first
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	if max < 1 {
		max = 1
	}
	return &rateLimiter{max: max, window: window}
}

// wait blocks until a slot is available within the window, then reserves it.
// It returns ctx.Err() if the context is cancelled while waiting.
func (rl *rateLimiter) wait(ctx context.Context) error {
	for {
		rl.mu.Lock()
		now := time.Now()
		rl.prune(now)
		if len(rl.stamps) < rl.max {
			rl.stamps = append(rl.stamps, now)
			rl.mu.Unlock()
			return nil
		}
		// At the ceiling: wait until the oldest reservation leaves the window.
		sleep := rl.stamps[0].Add(rl.window).Sub(now)
		rl.mu.Unlock()

		if sleep <= 0 {
			continue // window already advanced; re-evaluate
		}
		timer := time.NewTimer(sleep)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

// prune drops timestamps that have fallen out of the window. Caller holds the lock.
func (rl *rateLimiter) prune(now time.Time) {
	cutoff := now.Add(-rl.window)
	n := 0
	for _, t := range rl.stamps {
		if t.After(cutoff) {
			rl.stamps[n] = t
			n++
		}
	}
	rl.stamps = rl.stamps[:n]
}
