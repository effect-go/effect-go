//go:build egolayers

package bad

import (
	"github.com/effect-go/effect-go/layer"
	"github.com/effect-go/effect-go/scope"
)

func Missing() (*Service, error) {
	panic(layer.Build(NewService)) // ERROR "Missing: no provider for \*DB, needed by NewService"
}

func Ambiguous() (*Service, error) {
	panic(layer.Build(NewRepoService, Set)) // ERROR "several providers implement Repo"
}

func Cycle() (Loop1, error) {
	panic(layer.Build(NewLoop1, NewLoop2)) // ERROR "dependency cycle"
}

func NotNeeded() (A, error) {
	panic(layer.Build(NewA, Unused)) // ERROR "Unused is not needed"
}

func NoClose(s *scope.Scope) (*DB, error) {
	panic(layer.Build(layer.Close(NewDB))) // ERROR "layer.Close: \*DB has no Close method"
}

func TwoInputs(a A, b B) (*Service, error) {
	panic(layer.Build(NewRepoService)) // ERROR "several inputs implement Repo, needed by NewRepoService: a, b"
}
