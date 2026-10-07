//go:build egolayers

package shared

import "github.com/effect-go/effect-go/layer"

func Build() (*App, error) {
	panic(layer.Build(NewStore, NewA, NewB, NewApp))
}
