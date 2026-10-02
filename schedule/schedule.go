// Package schedule decides when to retry a failed task: how many times, how
// long to wait between attempts, and which errors are worth retrying.
//
// A Schedule is a plain value. Build one once, combine schedules with Min and
// Max, and share it between goroutines.
package schedule

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"time"
)

// Schedule is a retry policy. The zero Schedule never retries.
type Schedule struct {
	// step is called after the n-th failure (n starts at 1). It returns how
	// long to wait before the next attempt, and false to stop retrying.
	step func(n int, err error) (time.Duration, bool)
}

// Next reports the delay before the attempt that follows the n-th failure,
// and whether there should be one.
func (s Schedule) Next(n int, err error) (time.Duration, bool) {
	if s.step == nil {
		return 0, false
	}
	return s.step(n, err)
}

// Exponential retries forever, waiting base, then 2×base, 4×base, and so on.
// Combine it with Recurs or UpTo to bound it.
func Exponential(base time.Duration) Schedule {
	return Schedule{func(n int, _ error) (time.Duration, bool) {
		d := float64(base) * math.Pow(2, float64(n-1))
		if d > math.MaxInt64 {
			return math.MaxInt64, true
		}
		return time.Duration(d), true
	}}
}

// Spaced retries forever, waiting d between attempts.
func Spaced(d time.Duration) Schedule {
	return Schedule{func(_ int, _ error) (time.Duration, bool) { return d, true }}
}

// Recurs retries at most n times, without waiting.
func Recurs(n int) Schedule {
	return Schedule{func(i int, _ error) (time.Duration, bool) { return 0, i <= n }}
}

// Min continues while any of the schedules continues, and waits for the
// shortest of their delays.
func Min(ss ...Schedule) Schedule {
	return Schedule{func(n int, err error) (time.Duration, bool) {
		var best time.Duration
		ok := false
		for _, s := range ss {
			if d, more := s.Next(n, err); more && (!ok || d < best) {
				best, ok = d, true
			}
		}
		return best, ok
	}}
}

// Max continues while all the schedules continue, and waits for the longest
// of their delays. Max(Exponential(100*time.Millisecond), Recurs(5)) backs
// off exponentially, five times.
func Max(ss ...Schedule) Schedule {
	return Schedule{func(n int, err error) (time.Duration, bool) {
		var worst time.Duration
		for _, s := range ss {
			d, more := s.Next(n, err)
			if !more {
				return 0, false
			}
			worst = max(worst, d)
		}
		return worst, len(ss) > 0
	}}
}

// Jittered spreads each delay randomly between 80% and 120%, so clients that
// failed together don't retry together.
func (s Schedule) Jittered() Schedule {
	return Schedule{func(n int, err error) (time.Duration, bool) {
		d, ok := s.Next(n, err)
		return time.Duration(float64(d) * (0.8 + 0.4*rand.Float64())), ok
	}}
}

// While retries only errors for which retryable returns true. Use it to stop
// on errors that won't go away, such as a rejected request.
func (s Schedule) While(retryable func(error) bool) Schedule {
	return Schedule{func(n int, err error) (time.Duration, bool) {
		if !retryable(err) {
			return 0, false
		}
		return s.Next(n, err)
	}}
}

// UpTo caps each delay at d.
func (s Schedule) UpTo(d time.Duration) Schedule {
	return Schedule{func(n int, err error) (time.Duration, bool) {
		next, ok := s.Next(n, err)
		return min(next, d), ok
	}}
}

// Retry calls task until it succeeds or the schedule stops, and returns the
// last error. It stops at once if ctx is cancelled, including while waiting
// between attempts. It waits with time.Timer, so tests can run it on fake
// time with testing/synctest.
func Retry[T any](ctx context.Context, s Schedule, task func(context.Context) (T, error)) (T, error) {
	var zero T
	for n := 1; ; n++ {
		v, err := task(ctx)
		if err == nil {
			return v, nil
		}
		if ctx.Err() != nil {
			return zero, err
		}
		d, ok := s.Next(n, err)
		if !ok {
			return zero, err
		}
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return zero, fmt.Errorf("%w (retry stopped: %w)", err, context.Cause(ctx))
		case <-t.C:
		}
	}
}
