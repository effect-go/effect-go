package dashboard

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/effect-go/effect-go/scope"
)

type step[T any] struct {
	after time.Duration
	val   T
	err   error
	panic bool
}

type fake[T any] struct {
	mu       sync.Mutex
	steps    []step[T]
	calls    int
	canceled atomic.Int32
}

func script[T any](steps ...step[T]) *fake[T] { return &fake[T]{steps: steps} }

func (f *fake[T]) call(ctx context.Context) (T, error) {
	f.mu.Lock()
	s := f.steps[min(f.calls, len(f.steps)-1)]
	f.calls++
	f.mu.Unlock()
	if s.panic {
		var p *User
		_ = p.Name
	}
	select {
	case <-time.After(s.after):
		return s.val, s.err
	case <-ctx.Done():
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

func healthy() *world {
	return &world{
		users:   script(step[User]{after: 100 * time.Millisecond, val: User{ID: "u1", Banner: "b"}}),
		orders:  script(step[[]Order]{after: 200 * time.Millisecond, val: []Order{{ID: "o1"}}}),
		recs:    script(step[[]Product]{after: 150 * time.Millisecond, val: []Product{{ID: "p1"}}}),
		primary: script(step[Image]{after: 300 * time.Millisecond, val: Image{"primary"}}),
		mirror:  script(step[Image]{after: 50 * time.Millisecond, val: Image{"mirror"}}),
	}
}

func TestLoadsInParallel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := healthy()
		start := time.Now()
		page, err := Load(t.Context(), w.deps(), "u1")
		if err != nil || page.Banner.URL != "mirror" || len(page.Recs) != 1 {
			t.Fatalf("page %+v, err %v", page, err)
		}
		if took := time.Since(start); took != 250*time.Millisecond {
			t.Fatalf("took %v", took)
		}
	})
}

func TestFailureCancelsTheOthers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := healthy()
		w.users = script(step[User]{after: time.Second})
		w.orders = script(step[[]Order]{after: 10 * time.Millisecond, err: errors.New("orders down")})
		_, err := Load(t.Context(), w.deps(), "u1")
		if err == nil || w.users.canceled.Load() != 1 {
			t.Fatalf("err %v", err)
		}
	})
}

func TestRetriesButNotRejected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := healthy()
		flaky := errors.New("503")
		w.recs = script(step[[]Product]{err: flaky}, step[[]Product]{err: flaky}, step[[]Product]{val: []Product{{ID: "p1"}}})
		if _, err := Load(t.Context(), w.deps(), "u1"); err != nil || w.recs.calls != 3 {
			t.Fatalf("err %v after %d calls", err, w.recs.calls)
		}
		w = healthy()
		w.recs = script(step[[]Product]{err: ErrRejected})
		if _, err := Load(t.Context(), w.deps(), "u1"); !errors.Is(err, ErrRejected) || w.recs.calls != 1 {
			t.Fatalf("err %v after %d calls", err, w.recs.calls)
		}
	})
}

func TestTimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := healthy()
		w.orders = script(step[[]Order]{after: time.Hour})
		start := time.Now()
		_, err := Load(t.Context(), w.deps(), "u1")
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 2*time.Second {
			t.Fatalf("err %v after %v", err, time.Since(start))
		}
	})
}

func TestBannerLabel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := healthy()
		w.primary = script(step[Image]{err: errors.New("cdn1")})
		w.mirror = script(step[Image]{err: errors.New("cdn2")})
		_, err := Load(t.Context(), w.deps(), "u1")
		if err == nil || !strings.HasPrefix(err.Error(), "banner: ") {
			t.Fatalf("err %v", err)
		}
	})
}

func TestPanicReachesTheCaller(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := healthy()
		w.orders = script(step[[]Order]{panic: true})
		defer func() {
			if _, ok := recover().(*scope.Panic); !ok {
				t.Fatal("no *scope.Panic")
			}
		}()
		Load(t.Context(), w.deps(), "u1")
	})
}
