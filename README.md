# effect-go

**Reliable Go for the AI era.**

Typed errors, structured concurrency, retries, tracing and dependency wiring,
built in, in code that reads like what it does: no `if err != nil`, no `ctx`
passed by hand, no goroutines to keep track of. Build services your team can
ship, your users can rely on, and coding agents can work on.

Use it as a plain Go library, or write `.ego` files: Go plus a few keywords
that compile back to the Go you'd have written by hand.

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

Both errors come back, a panic re-panics in the caller with its original stack,
the right `ctx` is passed for you, and `Load` gets a tracing span.
`ego generate` turns this into [the Go you'd have written by hand](examples/dashboard/dashboard_ego.go),
which you commit like any other code.

## Try it

```bash
go install github.com/effect-go/effect-go/cmd/ego@latest
ego new example.com/hello
cd hello && ego test ./... && go run .
```

`ego new` creates a small HTTP service to explore.

## Built in

**Errors you can read, and can't forget to handle**
- `check f()` replaces `if err != nil { return err }`, and labels the error
  with the call that failed.
- Error sets list what a function can fail with, in its signature, and
  `match` makes sure you handle every case.
- Parallel calls give back every real failure, not just the first.

**Structured concurrency**
- `all`, `race`, `each` and `timeout` instead of goroutines, WaitGroups and
  errgroups, with limits: `each(ids, 8, users.Get)`.
- Nothing outlives the call: a failure cancels the rest, a panic reaches the
  caller, and resources close in reverse order ([`scope`](scope)).

**Retries and schedules**
- Policies are values: "back off exponentially, with jitter, at most 3 times"
  ([`schedule`](schedule)).
- `retry` a call, or `repeat` it as a loop that stops with its context.

**Context and tracing, passed for you**
- `effect` functions get their `ctx` and an OpenTelemetry span without you
  writing either ([`trace`](trace)).
- `slog` records carry the trace and span IDs.

**Dependency wiring**
- Constructors wired by generated code, checked when you run `ego generate`,
  with no reflection ([`layer`](layer)).
- Swap one provider in tests: `layer.Build(AppSet, NewMemRepo)`.

Everything except the syntax is a plain Go library
(`go get github.com/effect-go/effect-go`), and the shorthand is there if you
like it: `x => x.ID`, `f"{n} items"`, `a ?? b`.

## For coding agents

[AGENTS.md](AGENTS.md) is the whole language on one page. Agents write and
read less code, and the compiler checks what they'd get wrong: every error
case handled, the right `ctx` passed, nothing left running.

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

effect-go is young (v0.3) and meant to be used. A new release never
changes your build until you run `ego generate`. Before v1.0, breaking
changes only come in minor versions (v0.2, v0.3…), each with a
[changelog](CHANGELOG.md) entry saying how to update. New Go releases are
supported within a month.

Questions, ideas and bug reports are welcome in the issues. To contribute
code, see [CONTRIBUTING.md](CONTRIBUTING.md). The design notes are in
[docs/assessment.md](docs/assessment.md).

MIT licensed.
