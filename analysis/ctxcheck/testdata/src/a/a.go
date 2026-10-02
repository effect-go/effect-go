package a

import (
	"context"

	"github.com/effect-go/effect-go/scope"
	"golang.org/x/sync/errgroup"
)

func get(ctx context.Context) (int, error) { return 0, nil }

func group(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { _, err := get(ctx); return err }) // want "this goroutine uses ctx"
	g.Go(func() error { _, err := get(gctx); return err })
	return g.Wait()
}

func tasks(ctx context.Context) {
	outer := ctx
	scope.All2(ctx,
		func(ctx context.Context) (int, error) { return get(ctx) },
		func(c context.Context) (int, error) { return get(outer) }, // want "this task uses the outer outer"
	)
}
