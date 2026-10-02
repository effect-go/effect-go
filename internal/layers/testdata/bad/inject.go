//go:build egolayers

package bad

import "github.com/effect-go/effect-go/layer"

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
