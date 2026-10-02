//go:build egolayers

package app

import (
	"context"

	"github.com/effect-go/effect-go/layer"
	"github.com/effect-go/effect-go/scope"
)

// BuildApp builds the app for production.
func BuildApp(ctx context.Context, s *scope.Scope, cfg Config) (*App, error) {
	panic(layer.Build(AppSet))
}

// BuildTestApp builds the app with an in-memory repository.
func BuildTestApp(ctx context.Context, s *scope.Scope, cfg Config) (*App, error) {
	panic(layer.Build(AppSet, NewMemRepo))
}
