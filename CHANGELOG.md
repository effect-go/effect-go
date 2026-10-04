# Changelog

## v0.2.0 (2026-10-04)

**To update:** run `ego generate ./...` in each module (it now skips nested
modules, so run it inside them too), and rename any error-set case field
named `As`.

Breaking:
- Each error-set case gets an `As` method, so a pointer to a case
  (`&NotFound{}`) matches as the case in `match`, `else` arms and
  `errors.AsType`; it used to make a `match` without `_` panic. A case can no
  longer have a field named `As`.
- `ego generate ./...` skips nested modules, as `go` does.

Fixes:
- `scope.Run`: when the body panics and a fiber nobody joined panicked too,
  Run re-panics with the body's panic; it used to lose it.
- External test packages (`package p_test` in `_test.ego` files) can pass
  standard-library values across from the package under test; `ego generate`
  used to fail with errors like `http.Handler does not implement http.Handler`.
- `x := check f()` no longer clashes with an `err` declared later in the same
  block.
- `ego fmt` names the file in its errors.

Faster:
- `ego generate` loads the dependencies of all packages at once: 2.6 s to
  0.47 s on this repository, 2.25 s to 0.84 s on the miniflux port.
- `scope.Run` with `StopTimeout` closes a scope whose fibers have all stopped
  without a goroutine or a timer: 605 ns to 70 ns.

New:
- `check f() ""` returns the error as it is, without a label.
- `EGO_DEBUG=1 ego generate` prints the time each package spends in each
  phase.
- A GoLand plugin ([editors/jetbrains](editors/jetbrains)), and tested setups
  for Neovim and Vim ([docs/editors.md](docs/editors.md)). The VS Code
  extension (0.4.2) has icons, and no longer turns on file nesting in every
  workspace.
- The `ego` command and its language server are written in `.ego`.

## v0.1.0 (2026-10-02)

The first release.

- **Library** (plain Go): `scope` (`All2`–`All4`, `All`, `Each`, `Race`, `Timeout`, `Run`, `Fork`, `Acquire`, `Defer`, `Main`), `schedule` (retry policies, `Retry`, `Repeat`), `trace` (`Start`, `End`, `LogHandler`) and `layer`.
- **Dialect**: errors (`check`, `must`, `else`, `fail`), error sets and enums with exhaustive `match`, `effect` functions, `all`/`race`/`retry`/`repeat`/`timeout`/`each`, and shorthand.
- **The `ego` command**: `generate`, `test` (with `-cover`), `fmt`, `vet`, `lsp`, `new` and `eject`.
- **VS Code extension** 0.4.0.
