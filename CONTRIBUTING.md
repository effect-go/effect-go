# Contributing

Bug reports help most right now. Open an issue with the `.ego` code, what
`ego generate` or your editor did, and what you expected. For anything bigger
than a fix, open an issue first, so the design is agreed on before you write
the code.

## Building and testing

You need Go 1.26 or later.

```bash
go test ./...
```

Two kinds of tests skip themselves without what they need:
- the language server's, without gopls on the PATH (or `EGO_GOPLS` naming it);
- the todo example's PostgreSQL tests, without `TODO_TEST_DATABASE_URL`.
  The examples are a module of their own: run `go test ./...` in `examples`.

## Where things are

| Path | |
|---|---|
| `scope`, `schedule`, `trace`, `layer` | the library |
| `cmd/ego` | the command |
| `internal/syntax` | Go's scanner, parser, AST and printer, extended with the dialect |
| `internal/lower` | the compiler from `.ego` to Go |
| `internal/layers` | the wiring generator |
| `internal/lsp` | the gopls proxy |
| `analysis` | the `ego vet` analyzers |
| `editors` | the VS Code extension and the GoLand plugin |
| `internal/egotest` | dialect test packages, whose generated Go is committed and tested |
| `examples` | the users service and the dashboard, in plain Go and in the dialect, and [todo](examples/todo), a CLI on PostgreSQL |

## Changing the compiler

The compiler is `internal/lower`. The packages in `internal/egotest` and the
examples are `.ego` code whose generated Go is committed, and `TestGolden`
compares the two. After a change, regenerate them and look at the diff:

```bash
go install ./cmd/ego && ego generate ./... && (cd examples && ego generate ./...)
git diff -- '*_ego.go' '*_ego_test.go'
```

`ego generate ./...` skips nested modules, as `go` does, so `examples` is
generated from inside it.

`EGO_DEBUG=1 ego generate ./...` prints where the time goes for each
package, and the Go generated for packages that don't compile.
`go run ./experiments/generate-bench . examples` times `ego generate` on
whole modules, next to `go build`; `BenchmarkEditor` and `BenchmarkGenerate`
in `internal/lower` measure the editor's regeneration and the compiler.

A new compile error gets a case in `internal/lower/testdata/errors`: a line
ending with `// ERROR "regexp"` must get that error, and no other line any.

`cmd/ego` and `internal/lsp` are written in `.ego` themselves. `go build`
uses their committed Go, so a compiler change that breaks them can still be
built and fixed: regenerate them with a working `ego`.

`internal/syntax` is a copy of Go's parser and printer, extended with the
dialect. Its [README](internal/syntax/README.md) explains how a new Go
release is merged in.

## Style

- `gofmt` for Go, `ego fmt -w .` for `.ego` files.
- Comments and messages are plain English sentences. Compile errors say what
  to write instead.
- One change per commit, with a message that says why.
