# Changelog

## Unreleased

- `check f() ""` returns the error as it is, without a label.
- `x := check f()` no longer clashes with an `err` declared later in the same block.
- `ego fmt` names the file in its errors.
- The `ego` command and its language server are written in `.ego`.
- `scope.Run`: when the body panics and a fiber nobody joined panicked too, Run re-panics with the body's panic; it used to lose it.
- `scope.Run` with `StopTimeout` no longer starts a goroutine and a timer to close a scope whose fibers have all stopped: 605 ns to 70 ns.

## v0.1.0 (2026-10-02)

The first release.

- **Library** (plain Go): `scope` (`All2`–`All4`, `All`, `Each`, `Race`, `Timeout`, `Run`, `Fork`, `Acquire`, `Defer`, `Main`), `schedule` (retry policies, `Retry`, `Repeat`), `trace` (`Start`, `End`, `LogHandler`) and `layer`.
- **Dialect**: errors (`check`, `must`, `else`, `fail`), error sets and enums with exhaustive `match`, `effect` functions, `all`/`race`/`retry`/`repeat`/`timeout`/`each`, and shorthand.
- **The `ego` command**: `generate`, `test` (with `-cover`), `fmt`, `vet`, `lsp`, `new` and `eject`.
- **VS Code extension** 0.4.0.
