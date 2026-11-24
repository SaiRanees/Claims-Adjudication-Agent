package api

import (
	"sync"
	"time"
)

// The per-caller rate limit on registering files (and, from session 2, submitting claims):
// a burst of RateBurst requests, then one every RateInterval. A 50-file claim registered
// all at once takes about half a minute after the burst.
const (
	RateBurst    = 20
	RateInterval = time.Second
)

// minSweep is how many callers the limiter holds before it first drops idle ones.
const minSweep = 1024

// RateLimiter is a token bucket per caller, held in memory: one per API instance, which is
// enough for the demo. It runs the generic cell rate algorithm in whole durations, with no
// floating point. Each caller has the time at which its bucket is full again; a request
// that would push that time more than a burst's worth past now is refused, and a caller
// whose bucket is full again is forgotten at the next sweep.
type RateLimiter struct {
	mu sync.Mutex
	// interval is the time one token takes to come back.
	interval time.Duration
	// tolerance is how far past now a caller's full time may run: (burst - 1) intervals.
	tolerance time.Duration
	now       func() time.Time
	full      map[string]time.Time
	sweepAt   int
}

// NewRateLimiter returns a limiter that lets each caller make burst requests at once, then
// one every interval. now is the clock, time.Now outside tests.
func NewRateLimiter(burst int, interval time.Duration, now func() time.Time) *RateLimiter {
	burst = max(burst, 1)
	return &RateLimiter{
		interval:  interval,
		tolerance: time.Duration(burst-1) * interval,
		now:       now,
		full:      map[string]time.Time{},
		sweepAt:   minSweep,
	}
}

// Allow takes a token from caller's bucket. When the bucket is empty it returns false, and
// how long until a token comes back.
func (l *RateLimiter) Allow(caller string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	full, ok := l.full[caller]
	if !ok || full.Before(now) {
		full = now
	}
	if wait := full.Sub(now) - l.tolerance; wait > 0 {
		return false, wait
	}
	l.full[caller] = full.Add(l.interval)
	if len(l.full) >= l.sweepAt {
		l.sweep(now)
	}
	return true, 0
}

// sweep forgets the callers whose buckets are full again: they'd start afresh anyway.
func (l *RateLimiter) sweep(now time.Time) {
	for caller, full := range l.full {
		if !full.After(now) {
			delete(l.full, caller)
		}
	}
	l.sweepAt = max(minSweep, 2*len(l.full))
}

// retryAfter is a wait in whole seconds for a Retry-After header, rounded up, at least 1.
func retryAfter(wait time.Duration) int32 {
	seconds := (wait + time.Second - 1) / time.Second
	return int32(min(max(seconds, 1), 3600))
}
