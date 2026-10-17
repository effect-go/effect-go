//go:build egolayers

package tagged

import "github.com/effect-go/effect-go/layer"

func Build() (*App, error) {
	panic(layer.Build(NewApp))
}
