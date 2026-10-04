// Package scope runs tasks concurrently without leaking them. Every
// goroutine it starts has stopped by the time the call that started it
// returns, a failure cancels the work that depends on it, and a panic comes
// back to the caller with its original stack.
//
// Cancellation is cooperative, as everywhere in Go: a task stops early only
// if it watches its context.
package scope

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Scope owns the fibers and resources started inside Run. When Run returns,
// the scope interrupts the fibers that are still running, waits for them,
// and releases its resources in reverse order.
type Scope struct {
	parent   context.Context
	ctx      context.Context
	cancel   context.CancelCauseFunc
	wg       sync.WaitGroup
	mu       sync.Mutex
	closed   bool
	fibers   []joinable
	releases []func(context.Context) error
	stopIn   time.Duration // 0: wait for fibers indefinitely
}

// An Option configures Run.
type Option func(*Scope)

// StopTimeout bounds how long Run waits for fibers to stop once they are
// interrupted. Cancellation is cooperative, so a fiber that ignores its
// context can't be stopped: after d, Run releases the resources, returns a
// *StuckError joined to body's error, and leaves the stuck goroutines
// running. Without this option Run waits for them indefinitely.
func StopTimeout(d time.Duration) Option { return func(s *Scope) { s.stopIn = d } }

// A StuckError reports fibers that didn't stop within the StopTimeout.
type StuckError struct {
	Fibers int
	After  time.Duration
}

func (e *StuckError) Error() string {
	return fmt.Sprintf("scope: %d fiber(s) still running %v after being interrupted", e.Fibers, e.After)
}

// Context is the scope's context. It is cancelled when Run returns.
func (s *Scope) Context() context.Context { return s.ctx }

// Run calls body with a new scope, and closes the scope when body returns
// or panics. Errors from releasing resources are joined to body's error. If
// body panics, Run re-panics after closing the scope; otherwise it re-panics
// with the panic of a fiber nobody joined, if any.
func Run[T any](ctx context.Context, body func(s *Scope) (T, error), opts ...Option) (res T, err error) {
	s := &Scope{parent: ctx}
	for _, o := range opts {
		o(s)
	}
	s.ctx, s.cancel = context.WithCancelCause(ctx)
	defer func() {
		r := recover()
		unjoined, cerr := s.close()
		if r != nil {
			panic(r) // body's own panic comes first
		}
		if unjoined != nil {
			panic(unjoined)
		}
		if cerr != nil {
			err = errors.Join(err, cerr)
		}
	}()
	return body(s)
}

// close stops the fibers and releases the resources. It returns the panic
// of a fiber nobody joined, which would otherwise be lost, and the errors of
// releasing.
func (s *Scope) close() (*Panic, error) {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel(errClosed)
	var stuck error
	if s.stopIn > 0 {
		done := make(chan struct{})
		go func() { s.wg.Wait(); close(done) }()
		t := time.NewTimer(s.stopIn)
		select {
		case <-done:
			t.Stop()
		case <-t.C:
			n := 0
			for _, f := range s.fibers {
				if !f.stopped() {
					n++
				}
			}
			stuck = &StuckError{Fibers: n, After: s.stopIn}
		}
	} else {
		s.wg.Wait()
	}

	// Resources may need I/O to close, so they get a context that keeps the
	// parent's values but isn't cancelled.
	ctx := context.WithoutCancel(s.parent)
	var errs []error
	for i := len(s.releases) - 1; i >= 0; i-- {
		if err := s.releases[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}
	err := errors.Join(append([]error{stuck}, errs...)...)
	for _, f := range s.fibers {
		if p := f.unjoinedPanic(); p != nil {
			return p, err
		}
	}
	return nil, err
}

func (s *Scope) add() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		panic("scope: Fork after Run returned")
	}
	s.wg.Add(1)
}

type joinable interface {
	unjoinedPanic() *Panic
	stopped() bool
}

// Fiber is a task running in the background of a scope.
type Fiber[T any] struct {
	done   chan struct{}
	cancel context.CancelCauseFunc
	mu     sync.Mutex
	joined bool
	val    T
	err    error
}

// Fork starts task in the background. It stops when the scope closes, if
// it hasn't finished before.
func Fork[T any](s *Scope, task Task[T]) *Fiber[T] {
	s.add()
	ctx, cancel := context.WithCancelCause(s.ctx)
	f := &Fiber[T]{done: make(chan struct{}), cancel: cancel}
	s.mu.Lock()
	s.fibers = append(s.fibers, f)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer close(f.done)
		defer cancel(nil)
		label(ctx)
		f.err = protect(ctx, func(ctx context.Context) (err error) { f.val, err = task(ctx); return })
	}()
	return f
}

// Join waits for the fiber and returns its result. If the fiber panicked,
// Join re-panics with a *Panic that carries the original stack.
func (f *Fiber[T]) Join() (T, error) {
	<-f.done
	f.mu.Lock()
	f.joined = true
	f.mu.Unlock()
	if p, ok := f.err.(*Panic); ok {
		panic(p)
	}
	return f.val, f.err
}

// Interrupt cancels the fiber with cause and waits for it to stop.
func (f *Fiber[T]) Interrupt(cause error) {
	if cause == nil {
		cause = ErrInterrupted
	}
	f.cancel(cause)
	<-f.done
}

// Done is closed when the fiber has stopped.
func (f *Fiber[T]) Done() <-chan struct{} { return f.done }

func (f *Fiber[T]) stopped() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

func (f *Fiber[T]) unjoinedPanic() *Panic {
	if !f.stopped() {
		return nil // stuck: its result isn't written yet
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.err.(*Panic); ok && !f.joined {
		return p
	}
	return nil
}

// Acquire opens a resource and registers release to run when the scope
// closes. Resources are released last-in first-out, after every fiber in the
// scope has stopped.
func Acquire[T any](s *Scope, open Task[T], release func(context.Context, T) error) (T, error) {
	v, err := open(s.ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	s.mu.Lock()
	s.releases = append(s.releases, func(ctx context.Context) error { return release(ctx, v) })
	s.mu.Unlock()
	return v, nil
}

// Defer registers release to run when the scope closes, with the resources:
// last-in first-out, after every fiber has stopped.
func (s *Scope) Defer(release func(context.Context) error) {
	s.mu.Lock()
	s.releases = append(s.releases, release)
	s.mu.Unlock()
}
