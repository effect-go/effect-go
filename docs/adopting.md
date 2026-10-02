# Adopting effect-go

Every step below is useful on its own, and you can stop at any of them. The
dialect compiles to plain Go that is committed, so leaving is one command.

## 1. The library, from plain Go

```bash
go get github.com/effect-go/effect-go
```

Replace an errgroup and a retry loop where they hurt most:

```go
// errgroup: the first error only; a panic kills the process; a goroutine
// can still use the parent ctx by mistake.
user, orders, err := scope.All2(ctx,
	func(ctx context.Context) (User, error) { return d.Users.Get(ctx, id) },
	func(ctx context.Context) ([]Order, error) { return d.Orders.ForUser(ctx, id) },
)
// every real failure is joined; a panic comes back with its stack; the
// other call is cancelled and awaited

recs, err := schedule.Retry(ctx, schedule.Max(schedule.Exponential(100*time.Millisecond), schedule.Recurs(3)),
	func(ctx context.Context) ([]Rec, error) { return d.Recs.For(ctx, id) })
```

## 2. The analyzers, in CI

```bash
go install github.com/effect-go/effect-go/cmd/ego@latest
ego vet ./...
```

`ego vet` runs `go vet`, then finds, in plain Go: goroutines of an errgroup
using the parent's ctx instead of the group's, switches that miss a case of
a sum type or an enum, and fibers never joined.

## 3. One file in the dialect

Every Go file is a valid `.ego` file. Rename one, and use the dialect where
it removes noise:

```bash
git mv service.go service.ego
ego generate ./...        # writes service_ego.go: plain Go, commit it
```

- **Review** the `.ego` file. Mark the generated files so GitHub collapses
  them in diffs: `*_ego.go linguist-generated=true` in `.gitattributes`.
- **CI**: `ego generate -check ./...` fails if a generated file is stale,
  and `test -z "$(ego fmt -l .)"` if a `.ego` file isn't formatted.
- **Tests**: `ego test ./...` regenerates, then runs `go test`. For
  coverage, `ego test -cover` (plain `go test -cover` reports positions
  `go tool cover` can't read).
- **Editors**: see [editors.md](editors.md).
- **Agents**: put [AGENTS.md](../AGENTS.md) in the repository; it is the
  whole language on one page.

Packages that import yours see ordinary Go: functions take a `ctx` first and
return `error`.

## 4. A new service

`ego new example.com/myservice` creates a small HTTP service with all of the
above: an `effect` handler, an error set, layers, graceful shutdown, `slog`
linked to traces, and a test.

## Leaving

```bash
ego eject ./...      # prints the plan
ego eject -w ./...   # x.ego becomes x.go, the Go it compiled to
```

The result is ordinary Go, without `//line` directives or "generated"
headers, that still imports the runtime library (`scope`, `trace`, ...). To
drop the library too, replace those calls with your own; they are small.
