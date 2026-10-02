package scope

import "context"

type Task[T any] = func(context.Context) (T, error)

func All2[A, B any](ctx context.Context, a Task[A], b Task[B]) (A, B, error) {
	var x A
	var y B
	return x, y, nil
}
