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
  The example is a module of its own: run `go test ./...` in `examples/todo`.

## Changing the compiler

The compiler is `internal/lower`. The packages in `internal/egotest` and the
examples are `.ego` code whose generated Go is committed, and `TestGolden`
compares the two. After a change, regenerate them and look at the diff:

```bash
go install ./cmd/ego && ego generate ./...
git diff -- '*_ego.go' '*_ego_test.go'
```

A new compile error gets a case in `internal/lower/testdata/errors`: a line
ending with `// ERROR "regexp"` must get that error, and no other line any.

`internal/syntax` is a copy of Go's parser and printer, extended with the
dialect. Its [README](internal/syntax/README.md) explains how a new Go
release is merged in.

## Style

- `gofmt` for Go, `ego fmt -w .` for `.ego` files.
- Comments and messages are plain English sentences. Compile errors say what
  to write instead.
- One change per commit, with a message that says why.
