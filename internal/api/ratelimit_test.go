package api

import (
	"fmt"
	"testing"
	"time"
)

func TestRetryAfterRoundsUpToWholeSeconds(t *testing.T) {
	for _, tc := range []struct {
		wait time.Duration
		want int32
	}{
		{0, 1},
		{time.Nanosecond, 1},
		{time.Second, 1},
		{time.Second + time.Millisecond, 2},
		{6500 * time.Millisecond, 7},
		{time.Hour, 3600},
		{48 * time.Hour, 3600},
	} {
		if got := retryAfter(tc.wait); got != tc.want {
			t.Errorf("retryAfter(%v) = %d, want %d", tc.wait, got, tc.want)
		}
	}
}

func TestRateLimiterForgetsCallersWhoseBucketsAreFull(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	l := NewRateLimiter(5, time.Second, func() time.Time { return now })
	for i := range minSweep - 1 {
		l.Allow(fmt.Sprint("idle-", i))
	}
	// A second later every idle caller's bucket is full again, so the next sweep drops them.
	now = now.Add(time.Second)
	if ok, _ := l.Allow("active"); !ok {
		t.Fatal("a new caller was refused")
	}
	if n := len(l.full); n != 1 {
		t.Errorf("the limiter holds %d callers after the sweep, want 1", n)
	}
	if l.sweepAt != minSweep {
		t.Errorf("the next sweep is at %d callers, want %d", l.sweepAt, minSweep)
	}
}
