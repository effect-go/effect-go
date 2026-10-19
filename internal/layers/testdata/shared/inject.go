//go:build egolayers

package shared

import (
	"github.com/effect-go/effect-go/internal/layers/testdata/shared/fw"
	mylib "github.com/effect-go/effect-go/internal/layers/testdata/shared/lib"
	"github.com/effect-go/effect-go/layer"
	"github.com/effect-go/effect-go/scope"
)

func Build() (*App, error) {
	panic(layer.Build(NewStore, NewA, NewB, NewApp))
}

func BuildService(s *scope.Scope) (*Service, error) {
	panic(layer.Build(RepoSet, MailSet, NewService))
}

func BuildA() (A, error) {
	panic(layer.Build(GetterSet, NewMem))
}

func BuildServer() (*Server, error) {
	panic(layer.Build(Defaults, NewServer))
}

func BuildBareServer() (*Server, error) {
	panic(layer.Build(NewServer))
}

func BuildRemote() (*Remote, error) {
	panic(layer.Build(fw.Set, mylib.Load, NewRemote))
}

func BuildReport(s *scope.Scope, c Cfg) (*Report, error) {
	panic(layer.Build(RepoSet, NewStore, layer.Decorate(WrapGetter), NewReport))
}

func BuildUnwrapped() (*Server, error) {
	panic(layer.Build(Wrappers, NewServer))
}
