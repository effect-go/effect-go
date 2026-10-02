package dashboard

import (
	"context"
	"errors"
	"fmt"
	"time"

	"effectgo/schedule"
	"effectgo/scope"
)

var recsPolicy = schedule.Max(schedule.Exponential(100*time.Millisecond), schedule.Recurs(3)).
	Jittered().
	While(func(err error) bool { return !errors.Is(err, ErrRejected) })

// Load builds the page with the effectgo runtime.
func Load(ctx context.Context, d Deps, id string) (Page, error) {
	return scope.Timeout(ctx, 2*time.Second, func(ctx context.Context) (Page, error) {
		user, orders, recs, err := scope.All3(ctx,
			func(ctx context.Context) (User, error) { return d.Users.Get(ctx, id) },
			func(ctx context.Context) ([]Order, error) { return d.Orders.ForUser(ctx, id) },
			func(ctx context.Context) ([]Product, error) {
				return schedule.Retry(ctx, recsPolicy, func(ctx context.Context) ([]Product, error) { return d.Recs.For(ctx, id) })
			},
		)
		if err != nil {
			return Page{}, err
		}
		banner, err := scope.Race(ctx,
			func(ctx context.Context) (Image, error) { return d.CDN.Primary(ctx, user.Banner) },
			func(ctx context.Context) (Image, error) { return d.CDN.Mirror(ctx, user.Banner) },
		)
		if err != nil {
			return Page{}, fmt.Errorf("banner: %w", err)
		}
		return Page{User: user, Orders: orders, Recs: recs, Banner: banner}, nil
	})
}
