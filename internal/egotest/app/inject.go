//go:build egolayers

package app

import (
	"context"

	"github.com/effect-go/effect-go/internal/egotest/framework"
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

// BuildClock builds from a set that another package declares.
func BuildClock(ctx context.Context, s *scope.Scope) (*framework.Clock, error) {
	panic(layer.Build(framework.Set))
}

// BuildReports builds the reports command: what only sending needs waits
// for the first Get.
func BuildReports(ctx context.Context, s *scope.Scope, cfg Config) (*Reports, error) {
	panic(layer.Build(ReportSet, layer.Decorate(NewTraced)))
}
