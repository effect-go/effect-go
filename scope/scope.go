// Package scope runs tasks concurrently without leaking them. The
// goroutines that All, Race, Each and Timeout start have stopped when they
// return, and the fibers forked in a scope have stopped when its Run
// returns (unless they outlive a StopTimeout). A failure cancels the work
// that depends on it, and a panic comes back to the caller with its
// original stack.
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
	closed   bool                  // no more fibers: close has begun
	released bool                  // no more resources: they were released
	fibers   map[joinable]struct{} // running, or holding a panic
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
// body panics, Run re-panics with the same value after closing the scope,
// and the errors of closing it (such as a StuckError) are dropped; otherwise
// it re-panics with the panic of a fiber nobody joined, if any.
func Run[T any](ctx context.Context, body func(s *Scope) (T, error), opts ...Option) (res T, err error) {
	s := &Scope{parent: ctx, fibers: map[joinable]struct{}{}}
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
// of a fiber nobody joined, or else of a release, which would otherwise be
// lost, and the errors of releasing.
func (s *Scope) close() (*Panic, error) {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel(errClosed)
	var stuck error
	if s.stopIn > 0 && s.running() > 0 {
		done := make(chan struct{})
		go func() { s.wg.Wait(); close(done) }()
		t := time.NewTimer(s.stopIn)
		select {
		case <-done:
			t.Stop()
		case <-t.C:
			stuck = &StuckError{Fibers: s.running(), After: s.stopIn}
		}
	} else {
		s.wg.Wait()
	}

	s.mu.Lock()
	s.released = true
	releases := s.releases
	var panicked *Panic
	for f := range s.fibers {
		if p := f.unjoinedPanic(); p != nil {
			panicked = p
			break
		}
	}
	s.mu.Unlock()
	// Resources may need I/O to close, so they get a context that keeps the
	// parent's values but isn't cancelled. A release that panics doesn't
	// stop the others.
	ctx := context.WithoutCancel(s.parent)
	errs := []error{stuck}
	for i := len(releases) - 1; i >= 0; i-- {
		err := protect(ctx, releases[i])
		if p, ok := err.(*Panic); ok {
			if panicked == nil {
				panicked = p
			}
			continue
		}
		errs = append(errs, err)
	}
	return panicked, errors.Join(errs...)
}

// running returns how many fibers are still running.
func (s *Scope) running() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for f := range s.fibers {
		if !f.stopped() {
			n++
		}
	}
	return n
}

// add registers a fiber about to start, reporting false if the scope is
// closing. Counting and listing it under one lock means close, once it has
// set closed, sees every fiber.
func (s *Scope) add(f joinable) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.wg.Add(1)
	s.fibers[f] = struct{}{}
	return true
}

// done forgets a fiber that stopped, unless it panicked: close may need to
// re-panic with it. A scope that runs for the whole program, as in Main,
// would otherwise keep every fiber it ever forked.
func (s *Scope) done(f joinable, err error) {
	if _, ok := err.(*Panic); ok {
		return
	}
	s.mu.Lock()
	delete(s.fibers, f)
	s.mu.Unlock()
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
// it hasn't finished before. Once the scope is closing, Fork starts
// nothing and returns a fiber already interrupted, so a loop that forks
// until its context ends needs no special case.
func Fork[T any](s *Scope, task Task[T]) *Fiber[T] {
	ctx, cancel := context.WithCancelCause(s.ctx)
	f := &Fiber[T]{done: make(chan struct{}), cancel: cancel}
	if !s.add(f) {
		cancel(nil)
		f.err = errClosed
		close(f.done)
		return f
	}
	go func() {
		defer s.wg.Done()
		defer close(f.done)
		defer cancel(nil)
		label(ctx)
		f.err = protect(ctx, func(ctx context.Context) (err error) { f.val, err = task(ctx); return })
		s.done(f, f.err)
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

// Interrupt cancels the fiber with cause and waits for it to stop. A fiber
// can't interrupt itself: it would wait for itself forever.
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
// scope has stopped. Once the scope has released its resources, Acquire
// opens nothing and fails with a cancellation.
func Acquire[T any](s *Scope, open Task[T], release func(context.Context, T) error) (T, error) {
	var zero T
	s.mu.Lock()
	released := s.released
	s.mu.Unlock()
	if released {
		return zero, errClosed
	}
	v, err := open(s.ctx)
	if err != nil {
		return zero, err
	}
	if !s.register(func(ctx context.Context) error { return release(ctx, v) }) {
		return zero, errClosed
	}
	return v, nil
}

// Defer registers release to run when the scope closes, with the resources:
// last-in first-out, after every fiber has stopped. Once the scope has
// released its resources, Defer runs release at once.
func (s *Scope) Defer(release func(context.Context) error) {
	s.register(release)
}

// register adds a release, or runs it at once if the resources are
// released already, reporting whether it was added.
func (s *Scope) register(release func(context.Context) error) bool {
	s.mu.Lock()
	if !s.released {
		s.releases = append(s.releases, release)
		s.mu.Unlock()
		return true
	}
	s.mu.Unlock()
	release(context.WithoutCancel(s.parent))
	return false
}
