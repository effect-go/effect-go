# effect-go

Structured concurrency, typed errors, retries, resources and checked dependency wiring for Go: a plain Go library, plus an optional dialect that removes the noise around them.

Loading a page from four services (parallel calls, a retried call, two CDNs raced, a 2-second budget) takes 72 lines with errgroup and backoff. Two calls failing at once report only the first error, a panic in one call crashes the process, and the losing CDN request is still running after the function returns. With effect-go:

```go
effect Load(d Deps, id string) (Page, error) {
	return timeout(2*time.Second, load(d, id))
}

effect load(d Deps, id string) (Page, error) {
	user, orders, recs := check all(d.Users.Get(id), d.Orders.ForUser(id), retry(recsRetry, d.Recs.For(id)))
	banner := check race(d.CDN.Primary(user.Banner), d.CDN.Mirror(user.Banner)) "banner"
	return Page{User: user, Orders: orders, Recs: recs, Banner: banner}, nil
}
```

Every failure is reported, a panic comes back to the caller with its stack, nothing outlives the call, and `ctx` can't be the wrong one. `ego generate` compiles this to [the Go you'd write by hand](examples/dashboard/dashboard_ego.go) with the library, and the [same tests](examples/dashboard/dashboard_test.go) run against all three versions.

## The library (plain Go)

```bash
go get github.com/effect-go/effect-go
```

| Package | What it gives you |
|---|---|
| [`scope`](scope) | `All2`–`All4` and `All` (parallel; the first failure cancels the rest; every real failure is kept), `Race` (losers cancelled and awaited), `Timeout`, and `Run`/`Fork`/`Acquire`/`Defer` for fibers and resources released in reverse order. Panics come back as `*scope.Panic` with the original stack. |
| [`schedule`](schedule) | Retry policies as values: `Exponential`, `Spaced`, `Recurs`, `Min`, `Max`, `.Jittered()`, `.While(retryable)`, `.UpTo(d)`, and `Retry`. Testable on fake time with `testing/synctest`. |
| [`trace`](trace) | One OpenTelemetry span per call: `ctx, span := trace.Start(ctx, name)` and `defer trace.End(span, &err)`, which also records panics. |
| [`layer`](layer) | Dependency graphs wired at build time, like wire, with lifecycles: each provider runs once, a missing provider is a build error, finalizers run in reverse order. |

The library needs Go 1.26 and depends only on OpenTelemetry.

## The dialect

`.ego` files are Go plus: `check`/`must`/`else`/`fail` for errors, error sets and `enum` with exhaustive `match`, `effect` functions (implicit `ctx` and a span), `all`/`race`/`retry`/`timeout`, short lambdas `x => …`, `if` and `match` as expressions, `f"…"` strings, and `?.`/`??`. [AGENTS.md](AGENTS.md) is the whole language on one page.

```bash
go install github.com/effect-go/effect-go/cmd/ego@latest
ego generate ./...   # x.ego -> x_ego.go, committed; layers_ego.go for injectors
ego test ./...       # ego generate, then go test with the same arguments
ego fmt -w .         # formats .ego files
```

The generated code is plain, gofmt'd Go with `//line` directives, so compiler errors, `go vet`, panics and the debugger point at the `.ego` source. Go code calls it like any other package.

Editors: `ego lsp` is a language server that runs gopls on the generated Go and maps positions back, for hover, go to definition, diagnostics, rename and format-on-save. [editors/vscode](editors/vscode) is a VS Code extension for it.

Analyzers for plain Go: `go vet -vettool=$(which egovet) ./...` checks exhaustive switches over sum types and enums, child tasks that use their parent's context (the errgroup `ctx`/`gctx` bug), and fibers that are never joined. Install with `go install github.com/effect-go/effect-go/cmd/egovet@latest`.

## Status

v0.1, experimental. What's built and tested:
- the runtime library;
- the compiler, formatter, language server and VS Code extension;
- layers and the analyzers;
- the demos in [examples](examples).

The plan, its gates and their results are in [docs/assessment.md](docs/assessment.md), [docs/milestone-1.md](docs/milestone-1.md) and [docs/milestones-2-7.md](docs/milestones-2-7.md). Known limitations:
- `?.` can't follow a call.
- Coverage reports point at generated lines (a `//line` limitation; `ego generate -lines=false` turns the directives off).
- The language server needs gopls on the PATH.

## Layout

| Path | |
|---|---|
| `scope`, `schedule`, `trace`, `layer` | the library |
| `cmd/ego`, `cmd/egovet` | the commands |
| `internal/syntax` | Go's scanner, parser, AST and printer, extended with the dialect |
| `internal/lower` | the compiler from `.ego` to Go |
| `internal/layers` | the wiring generator |
| `internal/lsp` | the gopls proxy |
| `analysis` | the vet analyzers |
| `internal/egotest` | dialect test packages: each `.ego` file's generated Go is committed and tested |
| `examples` | the users service and the dashboard, in plain Go and in the dialect (a separate module) |
