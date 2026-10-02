package a

import (
	"context"

	"github.com/effect-go/effect-go/scope"
)

func work(context.Context) (int, error) { return 1, nil }

func f(s *scope.Scope) {
	scope.Fork(s, work)     // want "the fiber is never joined"
	_ = scope.Fork(s, work) // want "the fiber is never joined"
	lost := scope.Fork(s, work) // want "fiber lost is never joined"
	println(lost != nil)
	kept := scope.Fork(s, work)
	kept.Join()
}
