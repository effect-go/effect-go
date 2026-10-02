//go:build egolayers

package main

import (
	"context"
	"net/http"

	"github.com/effect-go/effect-go/layer"
	"github.com/effect-go/effect-go/scope"
)

// App is the service's dependency graph. ego generate wires it into
// layers_ego.go; a missing or ambiguous provider is a build error.
var App = layer.Set(NewGreeter, NewHandler, NewServer)

// BuildServer builds the server and what it needs, each once.
func BuildServer(ctx context.Context, s *scope.Scope, cfg Config) (*http.Server, error) {
	panic(layer.Build(App))
}
