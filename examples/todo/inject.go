//go:build egolayers

package main

import (
	"context"

	"github.com/effect-go/effect-go/layer"
	"github.com/effect-go/effect-go/scope"
)

// App is the production graph: a pool closed with the scope, the Postgres
// store, the service and the CLI.
var App = layer.Set(layer.Close(Connect), NewPgStore, NewClock, NewService, NewCLI)

// BuildCLI builds the CLI on PostgreSQL.
func BuildCLI(ctx context.Context, s *scope.Scope, cfg Config) (*CLI, error) {
	panic(layer.Build(App))
}

// BuildTestCLI swaps the database for memory and the clock for clock.
func BuildTestCLI(ctx context.Context, s *scope.Scope, cfg Config, clock Clock) (*CLI, error) {
	panic(layer.Build(App, NewMemStore))
}
