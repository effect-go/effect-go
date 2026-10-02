package schedule

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func delays(s Schedule, n int) []time.Duration {
	var out []time.Duration
	for i := 1; i <= n; i++ {
		d, ok := s.Next(i, nil)
		if !ok {
			break
		}
		out = append(out, d)
	}
	return out
}

func equal(a, b []time.Duration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestShapes(t *testing.T) {
	ms := time.Millisecond
	cases := []struct {
		name string
		s    Schedule
		want []time.Duration
	}{
		{"zero never retries", Schedule{}, nil},
		{"exponential", Exponential(100 * ms), []time.Duration{100 * ms, 200 * ms, 400 * ms, 800 * ms}},
		{"recurs", Recurs(2), []time.Duration{0, 0}},
		{"max: exponential, 3 times", Max(Exponential(100*ms), Recurs(3)), []time.Duration{100 * ms, 200 * ms, 400 * ms}},
		{"min: fastest delay while any continues", Min(Spaced(300*ms), Max(Exponential(100*ms), Recurs(2))), []time.Duration{100 * ms, 200 * ms, 300 * ms, 300 * ms}},
		{"capped", Max(Exponential(100*ms), Recurs(4)).UpTo(250 * ms), []time.Duration{100 * ms, 200 * ms, 250 * ms, 250 * ms}},
	}
	for _, c := range cases {
		if got := delays(c.s, 4); !equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestJittered(t *testing.T) {
	s := Spaced(time.Second).Jittered()
	for range 100 {
		d, _ := s.Next(1, nil)
		if d < 800*time.Millisecond || d > 1200*time.Millisecond {
			t.Fatalf("delay %v outside 80–120%%", d)
		}
	}
}

var (
	errFlaky    = errors.New("flaky")
	errRejected = errors.New("rejected")
)

func TestRetrySucceedsAfterFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		var waits []time.Duration
		policy := Max(Exponential(100*time.Millisecond), Recurs(5)).Tap(func(n int, err error, wait time.Duration) {
			waits = append(waits, wait)
		})
		rec := tracetest.NewSpanRecorder()
		ctx, span := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)).Tracer("test").Start(t.Context(), "call")
		start := time.Now()
		v, err := Retry(ctx, policy, func(context.Context) (string, error) {
			calls++
			if calls < 3 {
				return "", errFlaky
			}
			return "ok", nil
		})
		span.End()
		if err != nil || v != "ok" || calls != 3 {
			t.Fatalf("got %q %v after %d calls", v, err, calls)
		}
		if took := time.Since(start); took != 300*time.Millisecond {
			t.Fatalf("waited %v, want 100ms + 200ms", took)
		}
		if !slices.Equal(waits, []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}) {
			t.Fatalf("Tap saw %v", waits)
		}
		if ev := rec.Ended()[0].Events(); len(ev) != 2 || ev[1].Name != "retry" {
			t.Fatalf("span events %v, want one per retry", ev)
		}
	})
}

func TestRetryStopsOnPermanentError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		policy := Spaced(time.Second).While(func(err error) bool { return !errors.Is(err, errRejected) })
		_, err := Retry(t.Context(), policy, func(context.Context) (int, error) { calls++; return 0, errRejected })
		if !errors.Is(err, errRejected) || calls != 1 {
			t.Fatalf("err %v after %d calls", err, calls)
		}
	})
}

func TestRetryGivesUpWithLastError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		_, err := Retry(t.Context(), Recurs(2), func(context.Context) (int, error) { calls++; return 0, errFlaky })
		if !errors.Is(err, errFlaky) || calls != 3 {
			t.Fatalf("err %v after %d calls", err, calls)
		}
	})
}

func TestRetryStopsWhenCancelledWhileWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		time.AfterFunc(150*time.Millisecond, cancel)
		start := time.Now()
		_, err := Retry(ctx, Spaced(time.Hour), func(context.Context) (int, error) { return 0, errFlaky })
		if !errors.Is(err, errFlaky) || !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
		if took := time.Since(start); took != 150*time.Millisecond {
			t.Fatalf("took %v", took)
		}
	})
}

func TestRepeat(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var at []time.Duration
		start := time.Now()
		task := func(context.Context) (int, error) { at = append(at, time.Since(start)); return len(at), nil }
		n, err := Repeat(t.Context(), Max(Spaced(time.Minute), Recurs(2)), task)
		if err != nil || n != 3 || !slices.Equal(at, []time.Duration{0, time.Minute, 2 * time.Minute}) {
			t.Fatalf("got %d %v, calls at %v: want at once, then twice a minute apart", n, err, at)
		}

		// Delayed waits first, like a ticker, and cancellation ends it without an error.
		at, start = nil, time.Now()
		ctx, cancel := context.WithTimeout(t.Context(), 150*time.Second)
		defer cancel()
		n, err = Repeat(ctx, Spaced(time.Minute).Delayed(), task)
		if err != nil || n != 2 || !slices.Equal(at, []time.Duration{time.Minute, 2 * time.Minute}) {
			t.Fatalf("delayed: got %d %v, calls at %v", n, err, at)
		}

		// A failure stops it with the error.
		_, err = Repeat(t.Context(), Spaced(time.Second), func(context.Context) (int, error) { return 0, errFlaky })
		if err != errFlaky {
			t.Fatalf("failure: %v", err)
		}
	})
}
