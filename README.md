# effect-go

**Concurrent Go that cleans up after itself.** Run calls in parallel, retry
them, give them a deadline, and get every error back, without goroutines
left running or a `ctx` passed by mistake.

Use it as a plain Go library, or write `.ego` files: Go plus a few keywords
that compile back to ordinary Go.

## Before and after

Two calls in parallel, with errgroup:

```go
func Load(ctx context.Context, d Deps, id string) (Page, error) {
	g, gctx := errgroup.WithContext(ctx)
	var user User
	var orders []Order
	g.Go(func() (err error) {
		user, err = d.Users.Get(gctx, id)
		return err
	})
	g.Go(func() (err error) {
		orders, err = d.Orders.ForUser(gctx, id)
		return err
	})
	if err := g.Wait(); err != nil {
		return Page{}, err
	}
	return Page{User: user, Orders: orders}, nil
}
```

If both calls fail, you only see the first error. A panic in either one
crashes the whole program. And passing `ctx` instead of `gctx` compiles
fine, but then a failure doesn't cancel the other call.

The same in effect-go:

```go
effect Load(d Deps, id string) (Page, error) {
	user, orders := check all(d.Users.Get(id), d.Orders.ForUser(id))
	return Page{User: user, Orders: orders}, nil
}
```

Both errors come back, a panic returns to the caller with its stack trace,
the right `ctx` is passed for you, and `Load` gets a tracing span.
`ego generate` turns this into [the Go you'd have written by hand](examples/dashboard/dashboard_ego.go),
which you commit like any other code.

## Try it

```bash
go install github.com/effect-go/effect-go/cmd/ego@latest
ego new example.com/hello
cd hello && ego test ./... && go run .
```

`ego new` creates a small HTTP service to explore. [AGENTS.md](AGENTS.md)
is the whole language on one page, for you and for coding agents.

## What's in it

**The library**, for plain Go (`go get github.com/effect-go/effect-go`):
- [`scope`](scope): run calls in parallel (`All2`, `Each`, `Race`), with a
  deadline (`Timeout`), and close resources in order when you're done
  (`Run`, `Defer`, `Main`).
- [`schedule`](schedule): retry policies as values, like "back off
  exponentially, at most 3 times" (`Retry`), and loops that stop with
  their context (`Repeat`).
- [`trace`](trace): an OpenTelemetry span per call, and `slog` records that
  carry trace IDs.
- [`layer`](layer): dependency wiring checked at build time, for large
  graphs that tests vary.

**The dialect**, in `.ego` files:
- **Errors**: `check f()` returns the error for you; error sets list a
  service's failures, and `match` makes sure you handle each one.
- **Concurrency**: `effect` functions pass `ctx` for you, and
  `all`, `race`, `retry`, `repeat`, `timeout` and `each` replace
  hand-written goroutines.
- **Shorthand**, if you like it: `x => x.ID`, `f"{n} items"`, `a ?? b`.

## Safe to try

- **The output is plain Go.** It's committed next to your `.ego` files, and
  the rest of your code calls it like any other package.
- **Leaving is one command.** `ego eject -w ./...` turns your `.ego` files
  into ordinary `.go` files, for good.
- **Start small.** Use the library in one place, then turn one file into
  `.ego`. [docs/adopting.md](docs/adopting.md) shows the steps.
- **Your editor keeps working.** `ego lsp` gives `.ego` files gopls's hover,
  go to definition, diagnostics and rename. There's a
  [VS Code extension](editors/vscode), and [setup for other editors](docs/editors.md).
- **Errors point at your code.** Compiler errors, panics and the debugger
  show `.ego` lines.
- **We use it ourselves.** The `ego` command and its language server are
  written in `.ego`.

In a real codebase: we ported [miniflux's feed refreshing](docs/case-study-miniflux.md).
With one slow feed, its shutdown went from 18 seconds to instant.

## Status

effect-go is young (v0.1) and meant to be used. A new release never
changes your build until you run `ego generate`. Before v1.0, breaking
changes only come in minor versions (v0.2, v0.3…), each with a
[changelog](CHANGELOG.md) entry saying how to update. New Go releases are
supported within a month.

Questions, ideas and bug reports are welcome in the issues. To contribute
code, see [CONTRIBUTING.md](CONTRIBUTING.md). The design notes are in
[docs/assessment.md](docs/assessment.md).

MIT licensed.
