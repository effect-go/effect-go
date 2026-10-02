package scope

import (
	"context"
	"errors"
	"runtime/debug"
	"runtime/pprof"
	"sync"
	"time"
)

// A Task is a unit of work that should stop when its context is cancelled.
type Task[T any] = func(ctx context.Context) (T, error)

// protect runs fn and turns a panic into a *Panic error.
func protect(ctx context.Context, fn func(context.Context) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if p, ok := r.(*Panic); ok {
				err = p
				return
			}
			err = &Panic{Value: r, Stack: debug.Stack()}
		}
	}()
	return fn(ctx)
}

// label marks the current goroutine so goroutine dumps and the leak profile
// show it was started by this package.
func label(ctx context.Context) {
	pprof.SetGoroutineLabels(pprof.WithLabels(ctx, pprof.Labels("effectgo", "fiber")))
}

// group runs tasks that succeed or fail together: the first failure cancels
// the others, and wait returns only after every task has stopped.
type group struct {
	ctx     context.Context
	cancel  context.CancelCauseFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	errs    []error
	panic   *Panic
	stopped bool
}

func newGroup(ctx context.Context) *group {
	g := &group{}
	g.ctx, g.cancel = context.WithCancelCause(ctx)
	return g
}

func (g *group) spawn(run func(ctx context.Context) error) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		label(g.ctx)
		if err := protect(g.ctx, run); err != nil {
			g.fail(err)
		}
	}()
}

func (g *group) fail(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if p, ok := err.(*Panic); ok {
		if g.panic == nil {
			g.panic = p
		}
	} else if !g.stopped || !isInterrupt(err) {
		// After the group stops, cancellation errors come from the siblings
		// it cancelled. Real failures that raced with the first one are kept.
		g.errs = append(g.errs, err)
	}
	if !g.stopped {
		g.stopped = true
		g.cancel(errSibling)
	}
}

// wait blocks until every task has stopped, re-panics if one panicked, and
// otherwise returns the failures: one error, or several joined.
func (g *group) wait() error {
	g.wg.Wait()
	g.cancel(nil)
	if g.panic != nil {
		panic(g.panic)
	}
	switch len(g.errs) {
	case 0:
		return nil
	case 1:
		return g.errs[0]
	}
	return errors.Join(g.errs...)
}

// All runs tasks in parallel and returns their results in order. The first
// failure cancels the other tasks. All returns after every task has stopped,
// with every real failure (not the cancellations it caused) in the error.
// A panic in a task is re-raised in the caller as a *Panic.
func All[T any](ctx context.Context, tasks ...Task[T]) ([]T, error) {
	res := make([]T, len(tasks))
	g := newGroup(ctx)
	for i, t := range tasks {
		g.spawn(func(ctx context.Context) (err error) { res[i], err = t(ctx); return })
	}
	if err := g.wait(); err != nil {
		return nil, err
	}
	return res, nil
}

// All2 is All for two tasks with different result types.
func All2[A, B any](ctx context.Context, a Task[A], b Task[B]) (A, B, error) {
	var ra A
	var rb B
	g := newGroup(ctx)
	g.spawn(func(ctx context.Context) (err error) { ra, err = a(ctx); return })
	g.spawn(func(ctx context.Context) (err error) { rb, err = b(ctx); return })
	if err := g.wait(); err != nil {
		var za A
		var zb B
		return za, zb, err
	}
	return ra, rb, nil
}

// All3 is All for three tasks with different result types.
func All3[A, B, C any](ctx context.Context, a Task[A], b Task[B], c Task[C]) (A, B, C, error) {
	var ra A
	var rb B
	var rc C
	g := newGroup(ctx)
	g.spawn(func(ctx context.Context) (err error) { ra, err = a(ctx); return })
	g.spawn(func(ctx context.Context) (err error) { rb, err = b(ctx); return })
	g.spawn(func(ctx context.Context) (err error) { rc, err = c(ctx); return })
	if err := g.wait(); err != nil {
		var za A
		var zb B
		var zc C
		return za, zb, zc, err
	}
	return ra, rb, rc, nil
}

// All4 is All for four tasks with different result types.
func All4[A, B, C, D any](ctx context.Context, a Task[A], b Task[B], c Task[C], d Task[D]) (A, B, C, D, error) {
	var ra A
	var rb B
	var rc C
	var rd D
	g := newGroup(ctx)
	g.spawn(func(ctx context.Context) (err error) { ra, err = a(ctx); return })
	g.spawn(func(ctx context.Context) (err error) { rb, err = b(ctx); return })
	g.spawn(func(ctx context.Context) (err error) { rc, err = c(ctx); return })
	g.spawn(func(ctx context.Context) (err error) { rd, err = d(ctx); return })
	if err := g.wait(); err != nil {
		var za A
		var zb B
		var zc C
		var zd D
		return za, zb, zc, zd, err
	}
	return ra, rb, rc, rd, nil
}

// Race runs tasks in parallel and returns the first success. The losers are
// cancelled, and Race returns after all of them have stopped. If every task
// fails, Race returns all the failures. A panic is re-raised as a *Panic.
func Race[T any](ctx context.Context, tasks ...Task[T]) (T, error) {
	if len(tasks) == 0 {
		panic("scope.Race needs at least one task")
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		won  bool
		val  T
		errs []error
		pnc  *Panic
	)
	for _, t := range tasks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			label(ctx)
			var v T
			err := protect(ctx, func(ctx context.Context) (err error) { v, err = t(ctx); return })
			mu.Lock()
			defer mu.Unlock()
			if p, ok := err.(*Panic); ok {
				if pnc == nil {
					pnc = p
					cancel(errSibling)
				}
				return
			}
			switch {
			case err == nil && !won:
				won, val = true, v
				cancel(errLost)
			case err != nil && !(won && isInterrupt(err)):
				errs = append(errs, err)
			}
		}()
	}
	wg.Wait()
	if pnc != nil {
		panic(pnc)
	}
	if won {
		return val, nil
	}
	var zero T
	if len(errs) == 1 {
		return zero, errs[0]
	}
	return zero, errors.Join(errs...)
}

// Timeout runs task with a deadline. If the task stops because the deadline
// passed, Timeout returns a *TimeoutError. Cancellation is cooperative: a
// task that ignores its context runs to completion.
func Timeout[T any](ctx context.Context, d time.Duration, task Task[T]) (T, error) {
	terr := &TimeoutError{After: d}
	ctx, cancel := context.WithTimeoutCause(ctx, d, terr)
	defer cancel()
	v, err := task(ctx)
	if err != nil && ctx.Err() != nil && context.Cause(ctx) == terr {
		var zero T
		return zero, terr
	}
	return v, err
}
