package schedule

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
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
		start := time.Now()
		v, err := Retry(t.Context(), Max(Exponential(100*time.Millisecond), Recurs(5)), func(context.Context) (string, error) {
			calls++
			if calls < 3 {
				return "", errFlaky
			}
			return "ok", nil
		})
		if err != nil || v != "ok" || calls != 3 {
			t.Fatalf("got %q %v after %d calls", v, err, calls)
		}
		if took := time.Since(start); took != 300*time.Millisecond {
			t.Fatalf("waited %v, want 100ms + 200ms", took)
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
