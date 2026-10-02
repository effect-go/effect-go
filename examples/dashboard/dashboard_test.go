package dashboard

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/effect-go/effect-go/scope"
	"golang.org/x/sync/errgroup"
)

// step is what a fake service does on one call.
type step[T any] struct {
	after time.Duration
	val   T
	err   error
	panic bool
	// slowStop is how long the call takes to notice cancellation.
	slowStop time.Duration
}

// fake plays its steps in order, repeating the last one, and counts calls.
type fake[T any] struct {
	mu       sync.Mutex
	steps    []step[T]
	calls    int
	running  atomic.Int32
	canceled atomic.Int32
}

func script[T any](steps ...step[T]) *fake[T] { return &fake[T]{steps: steps} }

func (f *fake[T]) call(ctx context.Context) (T, error) {
	f.mu.Lock()
	s := f.steps[min(f.calls, len(f.steps)-1)]
	f.calls++
	f.mu.Unlock()
	f.running.Add(1)
	defer f.running.Add(-1)
	if s.panic {
		var p *User
		_ = p.Name // a nil pointer, as in real bugs
	}
	select {
	case <-time.After(s.after):
		return s.val, s.err
	case <-ctx.Done():
		time.Sleep(s.slowStop)
		f.canceled.Add(1)
		var zero T
		return zero, ctx.Err()
	}
}

type world struct {
	users           *fake[User]
	orders          *fake[[]Order]
	recs            *fake[[]Product]
	primary, mirror *fake[Image]
}

func (w *world) Get(ctx context.Context, _ string) (User, error)        { return w.users.call(ctx) }
func (w *world) ForUser(ctx context.Context, _ string) ([]Order, error) { return w.orders.call(ctx) }
func (w *world) For(ctx context.Context, _ string) ([]Product, error)   { return w.recs.call(ctx) }
func (w *world) Primary(ctx context.Context, _ string) (Image, error)   { return w.primary.call(ctx) }
func (w *world) Mirror(ctx context.Context, _ string) (Image, error)    { return w.mirror.call(ctx) }
func (w *world) deps() Deps                                             { return Deps{w, w, w, w} }

// healthy returns services that all answer, at different speeds.
func healthy() *world {
	return &world{
		users:   script(step[User]{after: 100 * time.Millisecond, val: User{ID: "u1", Banner: "b"}}),
		orders:  script(step[[]Order]{after: 200 * time.Millisecond, val: []Order{{ID: "o1"}}}),
		recs:    script(step[[]Product]{after: 150 * time.Millisecond, val: []Product{{ID: "p1"}}}),
		primary: script(step[Image]{after: 300 * time.Millisecond, val: Image{"primary"}}),
		mirror:  script(step[Image]{after: 50 * time.Millisecond, val: Image{"mirror"}}),
	}
}

type loader func(context.Context, Deps, string) (Page, error)

var impls = []struct {
	name string
	load loader
}{{"errgroup+backoff", LoadBaseline}, {"effect-go", Load}, {"effect-go dialect", LoadEgo}}

// each runs fn for both implementations, in a synctest bubble: time is fake,
// and a goroutine still blocked at the end fails the test.
func each(t *testing.T, fn func(t *testing.T, load loader, name string)) {
	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) { fn(t, impl.load, impl.name) })
		})
	}
}

func TestLoadsInParallel(t *testing.T) {
	each(t, func(t *testing.T, load loader, _ string) {
		w := healthy()
		start := time.Now()
		page, err := load(t.Context(), w.deps(), "u1")
		if err != nil || page.User.ID != "u1" || len(page.Orders) != 1 || len(page.Recs) != 1 || page.Banner.URL != "mirror" {
			t.Fatalf("page %+v, err %v", page, err)
		}
		// The slowest of the three parallel calls (200ms), then the fastest CDN (50ms).
		if took := time.Since(start); took != 250*time.Millisecond {
			t.Fatalf("took %v", took)
		}
	})
}

func TestFailureCancelsTheOtherCalls(t *testing.T) {
	each(t, func(t *testing.T, load loader, _ string) {
		w := healthy()
		w.users = script(step[User]{after: time.Second})
		w.orders = script(step[[]Order]{after: 10 * time.Millisecond, err: errors.New("orders down")})
		start := time.Now()
		_, err := load(t.Context(), w.deps(), "u1")
		if err == nil || time.Since(start) != 10*time.Millisecond {
			t.Fatalf("err %v after %v", err, time.Since(start))
		}
		if w.users.canceled.Load() != 1 || w.recs.canceled.Load() != 1 {
			t.Fatal("the other calls weren't cancelled")
		}
	})
}

func TestRetriesFlakyRecommendations(t *testing.T) {
	each(t, func(t *testing.T, load loader, _ string) {
		w := healthy()
		flaky := errors.New("recs: 503")
		w.recs = script(step[[]Product]{err: flaky}, step[[]Product]{err: flaky}, step[[]Product]{val: []Product{{ID: "p1"}}})
		page, err := load(t.Context(), w.deps(), "u1")
		if err != nil || len(page.Recs) != 1 || w.recs.calls != 3 {
			t.Fatalf("err %v after %d calls", err, w.recs.calls)
		}
	})
}

func TestDoesNotRetryRejected(t *testing.T) {
	each(t, func(t *testing.T, load loader, _ string) {
		w := healthy()
		w.recs = script(step[[]Product]{err: ErrRejected})
		_, err := load(t.Context(), w.deps(), "u1")
		if !errors.Is(err, ErrRejected) || w.recs.calls != 1 {
			t.Fatalf("err %v after %d calls", err, w.recs.calls)
		}
	})
}

func TestTimesOut(t *testing.T) {
	each(t, func(t *testing.T, load loader, _ string) {
		w := healthy()
		w.orders = script(step[[]Order]{after: time.Hour})
		start := time.Now()
		_, err := load(t.Context(), w.deps(), "u1")
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 2*time.Second {
			t.Fatalf("err %v after %v", err, time.Since(start))
		}
	})
}

// The differences.

// errgroup keeps only the first error. effect-go keeps every real failure.
func TestReportsEveryFailure(t *testing.T) {
	each(t, func(t *testing.T, load loader, name string) {
		w := healthy()
		errUsers, errOrders := errors.New("users down"), errors.New("orders down")
		w.users = script(step[User]{after: 10 * time.Millisecond, err: errUsers})
		w.orders = script(step[[]Order]{after: 10 * time.Millisecond, err: errOrders})
		_, err := load(t.Context(), w.deps(), "u1")
		both := errors.Is(err, errUsers) && errors.Is(err, errOrders)
		if want := name != "errgroup+backoff"; both != want {
			t.Fatalf("%s: both failures reported = %v (err: %v)", name, both, err)
		}
	})
}

// The baseline returns while the losing CDN request is still running. With
// effect-go, nothing started by Load outlives it.
func TestNothingOutlivesTheCall(t *testing.T) {
	each(t, func(t *testing.T, load loader, name string) {
		w := healthy()
		w.primary = script(step[Image]{after: time.Second, slowStop: 100 * time.Millisecond})
		_, err := load(t.Context(), w.deps(), "u1")
		if err != nil {
			t.Fatal(err)
		}
		stillRunning := w.primary.running.Load() > 0
		if want := name == "errgroup+backoff"; stillRunning != want {
			t.Fatalf("%s: a request still running after Load returned = %v", name, stillRunning)
		}
		// Let the baseline's stray request finish; synctest fails the test if a
		// goroutine is still running when it ends.
		time.Sleep(time.Second)
	})
}

// A panic in a parallel call comes back to the caller, with its stack, where
// recovery middleware handles it. With errgroup it crashes the whole process:
// see TestBaselinePanicCrashesTheProcess.
func TestPanicReachesTheCaller(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := healthy()
		w.orders = script(step[[]Order]{panic: true})
		defer func() {
			p, ok := recover().(*scope.Panic)
			if !ok || !strings.Contains(string(p.Stack), "(*fake[...]).call") {
				t.Fatalf("recovered %v", p)
			}
		}()
		Load(t.Context(), w.deps(), "u1")
		t.Fatal("Load should have panicked")
	})
}

func TestBaselinePanicCrashesTheProcess(t *testing.T) {
	if os.Getenv("DASHBOARD_CRASH") == "1" {
		w := healthy()
		w.orders = script(step[[]Order]{panic: true})
		func() {
			defer func() { recover() }() // the recovery middleware can't help: the panic is on another goroutine
			LoadBaseline(context.Background(), w.deps(), "u1")
		}()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestBaselinePanicCrashesTheProcess$")
	cmd.Env = append(os.Environ(), "DASHBOARD_CRASH=1")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "nil pointer dereference") {
		t.Fatalf("expected the process to crash, got err %v:\n%s", err, out)
	}
}

// The classic errgroup bug: a call uses ctx instead of gctx, so it isn't
// cancelled when a sibling fails, and the page waits for it. With effect-go
// each task's ctx parameter shadows the outer one, so the bug can't be
// written by name.
func TestWrongContextBug(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := healthy()
		w.users = script(step[User]{after: time.Second})
		w.orders = script(step[[]Order]{after: 10 * time.Millisecond, err: errors.New("orders down")})
		ctx := t.Context()
		g, gctx := errgroup.WithContext(ctx)
		g.Go(func() error { _, err := w.Get(ctx, "u1"); return err }) // ctx: wrong
		g.Go(func() error { _, err := w.ForUser(gctx, "u1"); return err })
		start := time.Now()
		g.Wait()
		if took := time.Since(start); took != time.Second {
			t.Fatalf("took %v", took)
		}
	})
}
