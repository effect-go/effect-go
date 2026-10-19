// Package layer declares dependency graphs that ego generate wires at build
// time, the way Effect's layers do: each provider runs once, a missing
// provider is a build error, and resources are released in reverse order
// when the app's scope closes.
//
// An injector is a function whose body is only panic(layer.Build(...)), in
// a file built with the egolayers tag:
//
//	//go:build egolayers
//
//	func BuildApp(ctx context.Context, s *scope.Scope, cfg Config) (*App, error) {
//		panic(layer.Build(AppSet))
//	}
//
// ego generate writes the real BuildApp into layers_ego.go (built without
// the tag): plain Go that calls each provider in dependency order.
//
// A provider is an ordinary function, given by its name: NewDB, sql.Open
// or NewCache[Item], not a function literal or a call. Its parameters are
// what it needs: the injector's parameters, other providers' results,
// context.Context and *scope.Scope; a variadic one gets the provided slice,
// if any. It returns T, (T, error), or (T, cleanup, error) where cleanup is
// func(), func() error or func(context.Context) error. Wrap it in Close to
// have T's Close method called when the scope closes.
//
// A Set may be declared in another package, such as a framework's: its
// providers must then be exported. A provider in several sets is one
// provider.
//
// Providers passed to Build directly take precedence over those in a Set,
// so a test graph swaps one provider: layer.Build(AppSet, NewMemoryRepo),
// even if AppSet's provides the Repo interface that NewMemoryRepo's result
// implements. An interface parameter is satisfied by the one provided type
// that implements it.
//
// Decorate wraps a service: layer.Decorate(NewCachedRepo), where
// NewCachedRepo(inner Repo, c *Cache) Repo, gives every provider needing a
// Repo the cached one, and only NewCachedRepo the Repo it wraps. Several
// decorators of a type wrap in the order they are listed, sets flattened,
// the first nearest the original.
//
// A parameter of type Lazy[T] defers building T, and what only T needs, to
// its first Get: what only some code paths use doesn't slow down the others,
// or fail them. Its providers get the injector's context and scope.
package layer

import "sync"

// A Provider is a constructor function, or a ProviderSet.
type Provider any

// A ProviderSet groups providers so graphs can share them.
type ProviderSet struct{ providers []Provider }

// Set groups providers.
func Set(providers ...Provider) ProviderSet { return ProviderSet{providers} }

// Build marks an injector. It only appears in egolayers files, which are
// never compiled into the program.
func Build(providers ...Provider) string {
	panic("layer.Build: run ego generate; injector files are built with -tags egolayers only")
}

// Close marks a provider whose result is closed (its Close method called)
// when the scope closes.
func Close(provider Provider) Provider { return closer{provider} }

type closer struct{ p Provider }

// Decorate marks a provider that wraps the service of its own result type,
// which one of its parameters takes.
func Decorate(provider Provider) Provider { return decorator{provider} }

type decorator struct{ p Provider }

// A Lazy is a service built on its first Get, by the code ego generate
// writes. Its finalizers run when the scope closes, as an eager service's
// do. A zero Lazy panics on Get.
type Lazy[T any] struct{ c *lazy[T] }

type lazy[T any] struct {
	mu    sync.Mutex
	built bool
	v     T
	build func() (T, error)
}

// NewLazy returns a Lazy that build builds. Generated code calls it.
func NewLazy[T any](build func() (T, error)) Lazy[T] { return Lazy[T]{&lazy[T]{build: build}} }

// Get returns the service, building it on the first call; concurrent calls
// wait for that one. A failure isn't kept: the next call tries again,
// reusing what built fine.
func (l Lazy[T]) Get() (T, error) {
	c := l.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.built {
		v, err := c.build()
		if err != nil {
			return v, err
		}
		c.v, c.built, c.build = v, true, nil
	}
	return c.v, nil
}
