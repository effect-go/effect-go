package effbench

// Go 1.27 generic methods on a concrete type: fluent chaining becomes possible.

func (e Effect[A]) Then[B any](f func(A) Effect[B]) Effect[B] { return FlatMap(e, f) }

func (e Effect[A]) Map[B any](f func(A) B) Effect[B] { return Map(e, f) }
