// Package effbench compares candidate EffectGo runtime representations
// against plain Go. Throwaway experiment code.
package effbench

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
)

// Payload stands in for a realistic domain value (e.g. a User row).
type Payload struct {
	ID    int
	Name  string
	Score float64
}

var errBoom = errors.New("boom")

// step is the unit of "real work": a non-inlined call that can fail.
//
//go:noinline
func step(_ context.Context, p Payload, failAt int) (Payload, error) {
	if p.ID == failAt {
		return Payload{}, errBoom
	}
	p.ID++
	return p, nil
}

// ---------- 0. Plain Go (== what a direct-style lowering would emit) ----------

func Direct(ctx context.Context, n, failAt int) (Payload, error) {
	p := Payload{Name: "x"}
	var err error
	for i := 0; i < n; i++ {
		p, err = step(ctx, p, failAt)
		if err != nil {
			return Payload{}, err
		}
	}
	return p, nil
}

// DirectCtx adds an interruption check between steps, i.e. what an
// interpreter buys you, done the Go way.
func DirectCtx(ctx context.Context, n, failAt int) (Payload, error) {
	p := Payload{Name: "x"}
	var err error
	for i := 0; i < n; i++ {
		if err := ctx.Err(); err != nil {
			return Payload{}, err
		}
		p, err = step(ctx, p, failAt)
		if err != nil {
			return Payload{}, err
		}
	}
	return p, nil
}

// ---------- A. Effect as a function of context ----------

type Effect[A any] func(context.Context) (A, error)

func Succeed[A any](a A) Effect[A] {
	return func(context.Context) (A, error) { return a, nil }
}

func FlatMap[A, B any](e Effect[A], f func(A) Effect[B]) Effect[B] {
	return func(ctx context.Context) (B, error) {
		a, err := e(ctx)
		if err != nil {
			var zero B
			return zero, err
		}
		return f(a)(ctx)
	}
}

func Map[A, B any](e Effect[A], f func(A) B) Effect[B] {
	return func(ctx context.Context) (B, error) {
		a, err := e(ctx)
		if err != nil {
			var zero B
			return zero, err
		}
		return f(a), nil
	}
}

func stepEff(p Payload, failAt int) Effect[Payload] {
	return func(ctx context.Context) (Payload, error) { return step(ctx, p, failAt) }
}

// ChainA mirrors the brief's nested-FlatMap lowering: each continuation
// builds the next effect while running.
func ChainA(p Payload, n, failAt int) Effect[Payload] {
	if n == 0 {
		return Succeed(p)
	}
	return FlatMap(stepEff(p, failAt), func(q Payload) Effect[Payload] {
		return ChainA(q, n-1, failAt)
	})
}

// LeftChainA builds the whole description up front (reusable, e.g. for retry).
func LeftChainA(n, failAt int) Effect[Payload] {
	e := Succeed(Payload{Name: "x"})
	for i := 0; i < n; i++ {
		e = FlatMap(e, func(p Payload) Effect[Payload] { return stepEff(p, failAt) })
	}
	return e
}

// ---------- B. Instruction tree + interpreter loop ----------

type op interface{ isOp() }

type (
	opSucceed struct{ v any }
	opFail    struct{ err error }
	opSync    struct {
		f func(context.Context) (any, error)
	}
	opFlatMap struct {
		e op
		k func(any) op
	}
)

func (*opSucceed) isOp() {}
func (*opFail) isOp()    {}
func (*opSync) isOp()    {}
func (*opFlatMap) isOp() {}

type IO[A any] struct{ op op }

func IOSucceed[A any](a A) IO[A] { return IO[A]{&opSucceed{a}} }

func IOSync[A any](f func(context.Context) (A, error)) IO[A] {
	return IO[A]{&opSync{func(ctx context.Context) (any, error) {
		a, err := f(ctx)
		return a, err
	}}}
}

func IOFlatMap[A, B any](e IO[A], f func(A) IO[B]) IO[B] {
	return IO[B]{&opFlatMap{e.op, func(v any) op { return f(v.(A)).op }}}
}

var errInterrupted = errors.New("interrupted")

// RunIO is a deliberately minimal fiber loop: no supervision, tracing,
// fiber refs or yielding, so it is a lower bound on interpreter cost.
func RunIO[A any](ctx context.Context, e IO[A]) (A, error) {
	var zero A
	var conts []func(any) op
	cur := e.op
	for {
		if ctx.Err() != nil { // interruption point between instructions
			return zero, errInterrupted
		}
		switch o := cur.(type) {
		case *opSucceed:
			if len(conts) == 0 {
				return o.v.(A), nil
			}
			k := conts[len(conts)-1]
			conts = conts[:len(conts)-1]
			cur = k(o.v)
		case *opFail:
			return zero, o.err
		case *opSync:
			v, err := o.f(ctx)
			if err != nil {
				cur = &opFail{err}
			} else {
				cur = &opSucceed{v}
			}
		case *opFlatMap:
			conts = append(conts, o.k)
			cur = o.e
		}
	}
}

func stepIO(p Payload, failAt int) IO[Payload] {
	return IOSync(func(ctx context.Context) (Payload, error) { return step(ctx, p, failAt) })
}

func ChainB(p Payload, n, failAt int) IO[Payload] {
	if n == 0 {
		return IOSucceed(p)
	}
	return IOFlatMap(stepIO(p, failAt), func(q Payload) IO[Payload] {
		return ChainB(q, n-1, failAt)
	})
}

// ---------- C. Library-only direct style: Try panics, Gen recovers ----------

type abort struct{ err error }

func Try[A any](a A, err error) A {
	if err != nil {
		panic(&abort{err})
	}
	return a
}

func Gen[A any](body func() A) (res A, err error) {
	defer func() {
		if r := recover(); r != nil {
			if ab, ok := r.(*abort); ok {
				err = ab.err
				return
			}
			panic(r)
		}
	}()
	return body(), nil
}

func DirectGen(ctx context.Context, n, failAt int) (Payload, error) {
	return Gen(func() Payload {
		p := Payload{Name: "x"}
		for i := 0; i < n; i++ {
			p = Try(step(ctx, p, failAt))
		}
		return p
	})
}

// ---------- Fibers over goroutines ----------

// Die wraps a panic captured in a fiber.
type Die struct {
	Value any
	Stack []byte
}

func (d *Die) Error() string { return fmt.Sprintf("fiber panicked: %v", d.Value) }

type Fiber[A any] struct {
	done   chan struct{}
	val    A
	err    error
	cancel context.CancelCauseFunc
}

func Fork[A any](ctx context.Context, e Effect[A]) *Fiber[A] {
	ctx, cancel := context.WithCancelCause(ctx)
	f := &Fiber[A]{done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(f.done)
		defer func() {
			if r := recover(); r != nil {
				f.err = &Die{Value: r, Stack: debug.Stack()}
			}
		}()
		f.val, f.err = e(ctx)
	}()
	return f
}

func (f *Fiber[A]) Join() (A, error) {
	<-f.done
	f.cancel(nil)
	return f.val, f.err
}

func (f *Fiber[A]) Interrupt(cause error) { f.cancel(cause) }

// All3 runs three effects concurrently. The first failure cancels the
// siblings, and All3 returns only after every child has finished.
func All3[A, B, C any](ctx context.Context, ea Effect[A], eb Effect[B], ec Effect[C]) (a A, b B, c C, err error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var wg sync.WaitGroup
	var once sync.Once
	fail := func(e error) { once.Do(func() { err = e; cancel(e) }) }
	run := func(f func() error) {
		wg.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					fail(&Die{Value: r, Stack: debug.Stack()})
				}
			}()
			if e := f(); e != nil {
				fail(e)
			}
		})
	}
	run(func() (e error) { a, e = ea(ctx); return })
	run(func() (e error) { b, e = eb(ctx); return })
	run(func() (e error) { c, e = ec(ctx); return })
	wg.Wait()
	return
}
