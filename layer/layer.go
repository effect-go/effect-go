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
// A provider is an ordinary function. Its parameters are what it needs: the
// injector's parameters, other providers' results, context.Context and
// *scope.Scope. It returns T, (T, error), or (T, cleanup, error) where
// cleanup is func(), func() error or func(context.Context) error. Wrap it
// in Close to have T's Close method called when the scope closes.
//
// A Set may be declared in another package, such as a framework's: its
// providers must then be exported.
//
// Providers passed to Build directly take precedence over those in a Set,
// so a test graph swaps one provider: layer.Build(AppSet, NewMemoryRepo).
// An interface parameter is satisfied by the one provided type that
// implements it.
package layer

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
