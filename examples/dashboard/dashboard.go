// Package dashboard builds a user's home page from four services. It's
// written twice, with the same behaviour:
//
//   - baseline.go uses errgroup and cenkalti/backoff, as most Go services do today.
//   - effectgo.go uses the effectgo runtime (scope and schedule).
//
// The page loads the user, their orders and their recommendations in
// parallel, retries flaky recommendation calls, races two CDNs for the
// banner, and gives up after two seconds.
package dashboard

import (
	"context"
	"errors"
)

type User struct {
	ID     string
	Name   string
	Banner string
}

type Order struct {
	ID    string
	Total int
}

type Product struct{ ID string }

type Image struct{ URL string }

type Page struct {
	User   User
	Orders []Order
	Recs   []Product
	Banner Image
}

type Deps struct {
	Users interface {
		Get(ctx context.Context, id string) (User, error)
	}
	Orders interface {
		ForUser(ctx context.Context, id string) ([]Order, error)
	}
	Recs interface {
		For(ctx context.Context, id string) ([]Product, error)
	}
	CDN interface {
		Primary(ctx context.Context, key string) (Image, error)
		Mirror(ctx context.Context, key string) (Image, error)
	}
}

// ErrRejected is a recommendation error that retrying won't fix.
var ErrRejected = errors.New("recommendations: request rejected")
