package effbench

import (
	"context"
	"errors"
	"testing"
	"time"
)

var (
	sinkP   Payload
	sinkErr error
)

const steps = 10

func BenchmarkSequential(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, tc := range []struct {
		name   string
		failAt int
	}{{"ok", -1}, {"fail-at-5", 5}} {
		b.Run(tc.name+"/0-plain-go", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkP, sinkErr = Direct(ctx, steps, tc.failAt)
			}
		})
		b.Run(tc.name+"/0-plain-go+ctx-check", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkP, sinkErr = DirectCtx(ctx, steps, tc.failAt)
			}
		})
		b.Run(tc.name+"/A-closure-nested", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkP, sinkErr = ChainA(Payload{Name: "x"}, steps, tc.failAt)(ctx)
			}
		})
		prebuilt := LeftChainA(steps, tc.failAt)
		b.Run(tc.name+"/A-closure-prebuilt", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkP, sinkErr = prebuilt(ctx)
			}
		})
		b.Run(tc.name+"/B-interpreter", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkP, sinkErr = RunIO(ctx, ChainB(Payload{Name: "x"}, steps, tc.failAt))
			}
		})
		b.Run(tc.name+"/C-gen-panic", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkP, sinkErr = DirectGen(ctx, steps, tc.failAt)
			}
		})
	}
}

func work(ctx context.Context) (Payload, error) { return step(ctx, Payload{Name: "x"}, -1) }

func BenchmarkConcurrency(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := Effect[Payload](work)
	b.Run("3-sequential-calls", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sinkP, sinkErr = work(ctx)
			sinkP, sinkErr = work(ctx)
			sinkP, sinkErr = work(ctx)
		}
	})
	b.Run("go+chan", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			ch := make(chan Payload, 1)
			go func() { p, _ := work(ctx); ch <- p }()
			sinkP = <-ch
		}
	})
	b.Run("fork-join", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sinkP, sinkErr = Fork(ctx, e).Join()
		}
	})
	b.Run("all3-structured", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sinkP, _, _, sinkErr = All3(ctx, e, e, e)
		}
	})
	boom := Effect[Payload](func(context.Context) (Payload, error) { panic("boom") })
	b.Run("fork-join-panic-to-Die", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sinkP, sinkErr = Fork(ctx, boom).Join()
		}
	})
}

// Is deep composition stack-safe? Go stacks grow (1 GB max on 64-bit).
func TestDeepChains(t *testing.T) {
	ctx := context.Background()
	for _, n := range []int{10_000, 1_000_000} {
		start := time.Now()
		p, err := LeftChainA(n, -1)(ctx)
		t.Logf("A left-nested  depth=%-9d id=%-9d err=%v %v", n, p.ID, err, time.Since(start))

		start = time.Now()
		p, err = ChainA(Payload{}, n, -1)(ctx)
		t.Logf("A right-nested depth=%-9d id=%-9d err=%v %v", n, p.ID, err, time.Since(start))

		start = time.Now()
		p, err = RunIO(ctx, ChainB(Payload{}, n, -1))
		t.Logf("B interpreter  depth=%-9d id=%-9d err=%v %v", n, p.ID, err, time.Since(start))
	}
}

func TestGenericMethodChaining(t *testing.T) {
	name, err := stepEff(Payload{Name: "ada"}, -1).
		Then(func(p Payload) Effect[Payload] { return stepEff(p, -1) }).
		Map(func(p Payload) string { return p.Name })(context.Background())
	if err != nil || name != "ada" {
		t.Fatalf("got %q, %v", name, err)
	}
}

func TestFiberSemantics(t *testing.T) {
	ctx := context.Background()
	slow := Effect[Payload](func(ctx context.Context) (Payload, error) {
		<-ctx.Done() // cooperative: only stops because it watches ctx
		return Payload{}, context.Cause(ctx)
	})
	failing := Effect[Payload](func(context.Context) (Payload, error) { return Payload{}, errBoom })
	_, _, _, err := All3(ctx, slow, failing, slow)
	if !errors.Is(err, errBoom) {
		t.Fatalf("want boom, got %v", err)
	}
	panicky := Effect[Payload](func(context.Context) (Payload, error) { panic("kaboom") })
	_, err = Fork(ctx, panicky).Join()
	var die *Die
	if !errors.As(err, &die) {
		t.Fatalf("want Die, got %v", err)
	}
}
