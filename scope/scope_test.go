package scope

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// sleep waits for d or until ctx is cancelled, like a well-behaved call.
func sleep[T any](d time.Duration, v T, err error) Task[T] {
	return func(ctx context.Context) (T, error) {
		select {
		case <-time.After(d):
			return v, err
		case <-ctx.Done():
			var zero T
			return zero, context.Cause(ctx)
		}
	}
}

var (
	errA = errors.New("a failed")
	errB = errors.New("b failed")
)

// Every test runs in a synctest bubble: time is fake, and the test fails if
// a goroutine is still blocked when it ends, so leaks can't pass silently.

func TestAllRunsInParallel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		a, b, err := All2(t.Context(), sleep(100*time.Millisecond, 1, nil), sleep(300*time.Millisecond, "x", nil))
		if err != nil || a != 1 || b != "x" {
			t.Fatalf("got %v %q %v", a, b, err)
		}
		if took := time.Since(start); took != 300*time.Millisecond {
			t.Fatalf("took %v, want the slowest task's 300ms", took)
		}
	})
}

func TestAllCancelsSiblingsOnFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var cause error
		slow := func(ctx context.Context) (int, error) {
			<-ctx.Done()
			cause = context.Cause(ctx)
			return 0, cause
		}
		start := time.Now()
		_, _, err := All2(t.Context(), sleep(10*time.Millisecond, 0, errA), slow)
		if err != errA {
			t.Fatalf("err = %v, want only errA (the sibling's cancellation is not a failure)", err)
		}
		if took := time.Since(start); took != 10*time.Millisecond {
			t.Fatalf("took %v, want 10ms", took)
		}
		if !errors.Is(cause, errSibling) {
			t.Fatalf("sibling cancelled with %v", cause)
		}
	})
}

func TestEachKeepsOrderAndLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var running, most atomic.Int32
		start := time.Now()
		got, err := Each(t.Context(), []int{1, 2, 3, 4, 5, 6}, 2, func(ctx context.Context, n int) (int, error) {
			for n := running.Add(1); ; {
				if m := most.Load(); n <= m || most.CompareAndSwap(m, n) {
					break
				}
			}
			defer running.Add(-1)
			return sleep(time.Duration(n)*10*time.Millisecond, n*n, nil)(ctx)
		})
		if err != nil || !slices.Equal(got, []int{1, 4, 9, 16, 25, 36}) {
			t.Fatalf("got %v %v", got, err)
		}
		if most.Load() != 2 {
			t.Fatalf("%d calls at once, want 2", most.Load())
		}
		if took := time.Since(start); took != 120*time.Millisecond {
			t.Fatalf("took %v, want 120ms: two workers, each taking the next item", took)
		}
		if got, err := Each(t.Context(), []int(nil), 4, func(context.Context, int) (int, error) { return 0, errA }); got == nil || len(got) != 0 || err != nil {
			t.Fatalf("no items: %v %v", got, err)
		}
		// A limit below 1 means no limit: all six at once.
		start = time.Now()
		Each(t.Context(), []int{1, 1, 1, 1, 1, 1}, 0, func(ctx context.Context, n int) (int, error) {
			return sleep(10*time.Millisecond, n, nil)(ctx)
		})
		if took := time.Since(start); took != 10*time.Millisecond {
			t.Fatalf("limit 0 took %v, want 10ms", took)
		}
	})
}

func TestEachStopsAtTheFirstFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		_, err := Each(t.Context(), []int{1, 2, 3, 4, 5}, 2, func(ctx context.Context, n int) (int, error) {
			calls.Add(1)
			if n == 1 {
				return sleep(10*time.Millisecond, 0, errA)(ctx)
			}
			return sleep(time.Second, n, nil)(ctx)
		})
		if err != errA || calls.Load() != 2 {
			t.Fatalf("err %v after %d calls, want errA after 2: the others never start", err, calls.Load())
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := Each(ctx, []int{1}, 1, func(ctx context.Context, n int) (int, error) { return n, nil }); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled parent: %v", err)
		}
	})
}

func TestAllKeepsEveryRealFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, _, err := All2(t.Context(), sleep(10*time.Millisecond, 0, errA), sleep(10*time.Millisecond, 0, errB))
		if !errors.Is(err, errA) || !errors.Is(err, errB) {
			t.Fatalf("err = %v, want both failures", err)
		}
	})
}

func TestAllRepanicsWithOriginalStack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer func() {
			p, ok := recover().(*Panic)
			if !ok {
				t.Fatal("want a *Panic")
			}
			if p.Value != "boom" || !strings.Contains(string(p.Stack), "panicsInBranch") {
				t.Fatalf("value %v, stack:\n%s", p.Value, p.Stack)
			}
			if KindOf(p) != Die {
				t.Fatalf("KindOf = %v", KindOf(p))
			}
		}()
		All2(t.Context(), panicsInBranch, sleep(time.Second, 0, nil))
		t.Fatal("All2 should have panicked")
	})
}

func panicsInBranch(context.Context) (int, error) { panic("boom") }

func TestAllStopsOnParentCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		time.AfterFunc(50*time.Millisecond, cancel)
		_, err := All(ctx, sleep(time.Second, 1, nil), sleep(time.Second, 2, nil))
		if KindOf(err) != Interrupt {
			t.Fatalf("err = %v, kind %v", err, KindOf(err))
		}
	})
}

func TestRaceReturnsFirstSuccessAndCancelsLosers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var stopped atomic.Bool
		slow := func(ctx context.Context) (string, error) {
			<-ctx.Done()
			stopped.Store(true)
			return "", ctx.Err()
		}
		start := time.Now()
		v, err := Race(t.Context(), slow, sleep(50*time.Millisecond, "mirror", nil))
		if err != nil || v != "mirror" {
			t.Fatalf("got %q %v", v, err)
		}
		if took := time.Since(start); took != 50*time.Millisecond {
			t.Fatalf("took %v", took)
		}
		if !stopped.Load() {
			t.Fatal("Race returned before the loser stopped")
		}
	})
}

func TestRaceIgnoresEarlyFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		v, err := Race(t.Context(), sleep(10*time.Millisecond, "", errA), sleep(50*time.Millisecond, "ok", nil))
		if err != nil || v != "ok" {
			t.Fatalf("got %q %v", v, err)
		}
	})
}

// Cancelled from outside, Race reports the cancellation once.
func TestRaceCancelledFromOutside(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		time.AfterFunc(10*time.Millisecond, cancel)
		_, err := Race(ctx, sleep(time.Second, 0, nil), sleep(time.Second, 0, nil), sleep(time.Second, 0, nil))
		if err != context.Canceled {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestRaceReturnsAllFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, err := Race(t.Context(), sleep(10*time.Millisecond, 0, errA), sleep(20*time.Millisecond, 0, errB))
		if !errors.Is(err, errA) || !errors.Is(err, errB) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, err := Timeout(t.Context(), 100*time.Millisecond, sleep(time.Second, 1, nil))
		if _, ok := errors.AsType[*TimeoutError](err); !ok || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v", err)
		}
		if KindOf(err) != Fail {
			t.Fatalf("a timeout is a failure, got %v", KindOf(err))
		}
		v, err := Timeout(t.Context(), time.Second, sleep(100*time.Millisecond, 7, nil))
		if err != nil || v != 7 {
			t.Fatalf("got %v %v", v, err)
		}
		// A task that fails on its own after the deadline keeps its error.
		_, err = Timeout(t.Context(), 10*time.Millisecond, func(ctx context.Context) (int, error) {
			time.Sleep(20 * time.Millisecond)
			return 0, errA
		})
		if err != errA {
			t.Fatalf("own error: %v", err)
		}
	})
}

func TestRunInterruptsFibersAndReleasesInReverse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var order []string
		var interrupted atomic.Bool
		_, err := Run(t.Context(), func(s *Scope) (int, error) {
			for _, name := range []string{"db", "cache"} {
				Acquire(s,
					func(context.Context) (string, error) { return name, nil },
					func(_ context.Context, n string) error { order = append(order, n); return nil })
			}
			Fork(s, func(ctx context.Context) (int, error) {
				<-ctx.Done()
				interrupted.Store(true)
				return 0, ctx.Err()
			})
			return 1, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !interrupted.Load() {
			t.Fatal("Run returned before its fiber stopped")
		}
		if strings.Join(order, ",") != "cache,db" {
			t.Fatalf("release order %v", order)
		}
	})
}

func TestForkJoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		v, err := Run(t.Context(), func(s *Scope) (int, error) {
			f := Fork(s, sleep(10*time.Millisecond, 41, nil))
			n, err := f.Join()
			return n + 1, err
		})
		if err != nil || v != 42 {
			t.Fatalf("got %v %v", v, err)
		}
	})
}

func TestUnjoinedPanicIsNotLost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer func() {
			if _, ok := recover().(*Panic); !ok {
				t.Fatal("want the fiber's panic when the scope closes")
			}
		}()
		Run(t.Context(), func(s *Scope) (int, error) {
			Fork(s, panicsInBranch)
			time.Sleep(time.Millisecond)
			return 0, nil
		})
	})
}

// When body panics too, its own panic is the one that comes out.
func TestBodyPanicBeatsUnjoinedPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer func() {
			if r := recover(); r != "body" {
				t.Fatalf("recovered %v, want body's panic", r)
			}
		}()
		Run(t.Context(), func(s *Scope) (int, error) {
			Fork(s, panicsInBranch)
			time.Sleep(time.Millisecond)
			panic("body")
		})
	})
}

func TestInterrupt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		Run(t.Context(), func(s *Scope) (int, error) {
			f := Fork(s, sleep(time.Hour, 0, nil))
			f.Interrupt(nil)
			_, err := f.Join()
			if !errors.Is(err, ErrInterrupted) {
				t.Fatalf("err = %v", err)
			}
			return 0, nil
		})
	})
}

// Once the scope is closing, Fork starts nothing and returns an
// interrupted fiber: a fiber that forks until its context ends can race
// with the scope closing.
func TestForkOnClosingScope(t *testing.T) {
	var s *Scope
	Run(t.Context(), func(sc *Scope) (int, error) { s = sc; return 0, nil })
	if _, err := Fork(s, sleep(time.Hour, 0, nil)).Join(); KindOf(err) != Interrupt {
		t.Fatalf("Fork after Run: %v", err)
	}
	for range 200 {
		Run(t.Context(), func(s *Scope) (int, error) {
			Fork(s, func(ctx context.Context) (int, error) {
				for ctx.Err() == nil {
					Fork(s, func(context.Context) (int, error) { return 0, nil })
				}
				return 0, nil
			})
			return 0, nil
		})
	}
}

// A release that panics doesn't keep the others from running, and its
// panic comes back once they have.
func TestReleasePanic(t *testing.T) {
	released := false
	defer func() {
		if r := recover(); !released || r == nil {
			t.Fatalf("released %v, recovered %v", released, r)
		}
	}()
	Run(t.Context(), func(s *Scope) (int, error) {
		s.Defer(func(context.Context) error { released = true; return nil })
		s.Defer(func(context.Context) error { panic("release failed") })
		return 0, nil
	})
}

// After the scope released its resources, Defer releases at once and
// Acquire opens nothing.
func TestLateRelease(t *testing.T) {
	var s *Scope
	Run(t.Context(), func(sc *Scope) (int, error) { s = sc; return 0, nil })
	released, opened := false, false
	s.Defer(func(context.Context) error { released = true; return nil })
	_, err := Acquire(s, func(context.Context) (int, error) { opened = true; return 1, nil }, func(context.Context, int) error { return nil })
	if !released || opened || KindOf(err) != Interrupt {
		t.Fatalf("released %v, opened %v, err %v", released, opened, err)
	}
}

// A scope that lives as long as the program doesn't keep the fibers that
// finished.
func TestFinishedFibersAreForgotten(t *testing.T) {
	Run(t.Context(), func(s *Scope) (int, error) {
		for range 100 {
			Fork(s, func(context.Context) (int, error) { return 0, nil }).Join()
		}
		if n := len(s.fibers); n != 0 {
			t.Fatalf("%d finished fibers kept", n)
		}
		return 0, nil
	})
}

func TestStopTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		_, err := Run(t.Context(), func(s *Scope) (int, error) {
			Fork(s, func(ctx context.Context) (int, error) {
				time.Sleep(10 * time.Second) // ignores ctx
				return 0, nil
			})
			Fork(s, func(ctx context.Context) (int, error) {
				<-ctx.Done()
				return 0, ctx.Err()
			})
			return 1, nil
		}, StopTimeout(time.Second))
		var stuck *StuckError
		if !errors.As(err, &stuck) || stuck.Fibers != 1 || time.Since(start) != time.Second {
			t.Fatalf("err %v after %v", err, time.Since(start))
		}
		time.Sleep(10 * time.Second) // let the stuck fiber finish
	})
}
