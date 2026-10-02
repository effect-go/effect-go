package scope

import "context"

type Task[T any] = func(context.Context) (T, error)

func All2[A, B any](ctx context.Context, a Task[A], b Task[B]) (A, B, error) {
	var x A
	var y B
	return x, y, nil
}

type Scope struct{}
type Fiber[T any] struct{}

func Fork[T any](s *Scope, task Task[T]) *Fiber[T] { return nil }
func (f *Fiber[T]) Join() (T, error)            { var x T; return x, nil }
