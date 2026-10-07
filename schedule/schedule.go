// Package schedule decides when to retry a failed task: how many times, how
// long to wait between attempts, and which errors are worth retrying.
//
// A Schedule is a plain value. Build one once, combine schedules with Min and
// Max, and share it between goroutines.
package schedule

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Schedule is a retry or repeat policy. The zero Schedule never retries.
type Schedule struct {
	// step is called after the n-th attempt (n starts at 1), with its error
	// when retrying and nil when repeating. It returns how long to wait
	// before the next attempt, and false to stop.
	step func(n int, err error) (time.Duration, bool)
	// delayed: Repeat waits before the first call too.
	delayed bool
}

// Next reports the delay before the attempt that follows the n-th one, and
// whether there should be one. Retry passes the n-th failure's error;
// Repeat passes nil.
func (s Schedule) Next(n int, err error) (time.Duration, bool) {
	if s.step == nil {
		return 0, false
	}
	return s.step(n, err)
}

// Exponential retries forever, waiting base, then 2×base, 4×base, and so on.
// Combine it with Recurs or UpTo to bound it.
func Exponential(base time.Duration) Schedule {
	return Schedule{step: func(n int, _ error) (time.Duration, bool) {
		d := float64(base) * math.Pow(2, float64(n-1))
		if d > math.MaxInt64 {
			return math.MaxInt64, true
		}
		return time.Duration(d), true
	}}
}

// Spaced retries forever, waiting d between attempts.
func Spaced(d time.Duration) Schedule {
	return Schedule{step: func(_ int, _ error) (time.Duration, bool) { return d, true }}
}

// Recurs retries at most n times, without waiting.
func Recurs(n int) Schedule {
	return Schedule{step: func(i int, _ error) (time.Duration, bool) { return 0, i <= n }}
}

// Min continues while any of the schedules continues, and waits for the
// shortest of their delays.
func Min(ss ...Schedule) Schedule {
	return Schedule{delayed: anyDelayed(ss), step: func(n int, err error) (time.Duration, bool) {
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
	return Schedule{delayed: anyDelayed(ss), step: func(n int, err error) (time.Duration, bool) {
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

func anyDelayed(ss []Schedule) bool {
	for _, s := range ss {
		if s.delayed {
			return true
		}
	}
	return false
}

// with returns a schedule with another step, keeping Delayed.
func (s Schedule) with(step func(n int, err error) (time.Duration, bool)) Schedule {
	return Schedule{step: step, delayed: s.delayed}
}

// Jittered spreads each delay randomly between 80% and 120%, so clients that
// failed together don't retry together.
func (s Schedule) Jittered() Schedule {
	return s.with(func(n int, err error) (time.Duration, bool) {
		d, ok := s.Next(n, err)
		// Past math.MaxInt64, converting back to a Duration is undefined
		// (negative on amd64): saturate, as Exponential does.
		if f := float64(d) * (0.8 + 0.4*rand.Float64()); f < math.MaxInt64 {
			d = time.Duration(f)
		} else {
			d = math.MaxInt64
		}
		return d, ok
	})
}

// While retries only errors for which retryable returns true. Use it to stop
// on errors that won't go away, such as a rejected request. It doesn't
// apply to Repeat, which has no error to judge.
func (s Schedule) While(retryable func(error) bool) Schedule {
	return s.with(func(n int, err error) (time.Duration, bool) {
		if err != nil && !retryable(err) {
			return 0, false
		}
		return s.Next(n, err)
	})
}

// UpTo caps each delay at d.
func (s Schedule) UpTo(d time.Duration) Schedule {
	return s.with(func(n int, err error) (time.Duration, bool) {
		next, ok := s.Next(n, err)
		return min(next, d), ok
	})
}

// Tap calls fn before each retry with the number of failures so far, the
// last error and the delay before the next attempt: for logs and metrics.
// Put it last, after Jittered or UpTo, so it sees the delay Retry waits.
func (s Schedule) Tap(fn func(n int, err error, wait time.Duration)) Schedule {
	return s.with(func(n int, err error) (time.Duration, bool) {
		d, ok := s.Next(n, err)
		if ok {
			fn(n, err, d)
		}
		return d, ok
	})
}

// Delayed makes Repeat wait for the schedule's first delay before the first
// call too, as a time.Ticker does.
func (s Schedule) Delayed() Schedule {
	s.delayed = true
	return s
}

// Repeat calls task, then calls it again after each delay the schedule gives
// (asked with a nil error), until the schedule stops, task fails or ctx is
// cancelled. It returns task's last result, or its error. Cancellation ends
// a repetition without an error: a loop that runs until shutdown has done
// its job. Repeat(ctx, Spaced(time.Minute).Delayed(), task) is a ticker
// loop that stops with ctx.
func Repeat[T any](ctx context.Context, s Schedule, task func(context.Context) (T, error)) (T, error) {
	var last T
	delays := 0 // delays asked for so far: Recurs(3) allows three
	for first := true; ; first = false {
		if !first || s.delayed {
			delays++
			d, ok := s.Next(delays, nil)
			if !ok {
				return last, nil
			}
			t := time.NewTimer(d)
			select {
			case <-ctx.Done():
				t.Stop()
				return last, nil
			case <-t.C:
			}
		}
		if ctx.Err() != nil {
			return last, nil
		}
		v, err := task(ctx)
		if err != nil {
			// A call that ctx's end interrupted is a stop, not a failure.
			if ctx.Err() != nil && (errors.Is(err, ctx.Err()) || errors.Is(err, context.Cause(ctx))) {
				return last, nil
			}
			var zero T
			return zero, err
		}
		last = v
	}
}

// Retry calls task until it succeeds or the schedule stops, and returns the
// last error. It stops at once if ctx is cancelled, including while waiting
// between attempts. Each retry is an event on the span in ctx, with the
// error and the delay. Retry waits with time.Timer, so tests can run it on
// fake time with testing/synctest.
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
		oteltrace.SpanFromContext(ctx).AddEvent("retry", oteltrace.WithAttributes(
			attribute.Int("retry.failures", n),
			attribute.String("retry.error", err.Error()),
			attribute.String("retry.wait", d.String()),
		))
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return zero, fmt.Errorf("%w (retry stopped: %w)", err, context.Cause(ctx))
		case <-t.C:
		}
	}
}
