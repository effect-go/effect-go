package dashboard

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cenkalti/backoff/v5"
	"golang.org/x/sync/errgroup"
)

// LoadBaseline builds the page with errgroup and cenkalti/backoff.
func LoadBaseline(ctx context.Context, d Deps, id string) (Page, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var page Page
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		u, err := d.Users.Get(gctx, id)
		page.User = u
		return err
	})
	g.Go(func() error {
		o, err := d.Orders.ForUser(gctx, id)
		page.Orders = o
		return err
	})
	g.Go(func() error {
		b := backoff.NewExponentialBackOff()
		b.InitialInterval = 100 * time.Millisecond
		b.RandomizationFactor = 0.2
		recs, err := backoff.Retry(gctx, func() ([]Product, error) {
			recs, err := d.Recs.For(gctx, id)
			if errors.Is(err, ErrRejected) {
				return nil, backoff.Permanent(err)
			}
			return recs, err
		}, backoff.WithBackOff(b), backoff.WithMaxTries(4))
		page.Recs = recs
		return err
	})
	if err := g.Wait(); err != nil {
		return Page{}, err
	}

	banner, err := firstImage(ctx, page.User.Banner, d.CDN.Primary, d.CDN.Mirror)
	if err != nil {
		return Page{}, fmt.Errorf("banner: %w", err)
	}
	page.Banner = banner
	return page, nil
}

// firstImage returns the first image to load and cancels the other requests.
func firstImage(ctx context.Context, key string, fetchers ...func(context.Context, string) (Image, error)) (Image, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		img Image
		err error
	}
	results := make(chan result, len(fetchers)) // buffered, or the losers block forever
	for _, fetch := range fetchers {
		go func() {
			img, err := fetch(ctx, key)
			results <- result{img, err}
		}()
	}
	var errs []error
	for range fetchers {
		r := <-results
		if r.err == nil {
			return r.img, nil
		}
		errs = append(errs, r.err)
	}
	return Image{}, errors.Join(errs...)
}
