# Changelog

## v0.3.1 (2026-10-07)

- `must (x).M()` and `check (x).M()`, with a space, are the keywords before
  a parenthesized expression (also `&`, `*` and `[`); they were taken for
  calls of functions named `must` and `check`, and `ego fmt` then removed
  the space. `must(x)` still calls a function named `must`. Reported by
  goncini.

## v0.3.0 (2026-10-07)

From a review of the whole codebase. **To update:** run `ego generate
./...` in each module, and pass `1` to `scope.Each` (or `each`) where a
limit of `0` meant one at a time: it now means no limit.

Behavior changes:
- `check f() as Case` lets errors that already are cases of the
  function's set through unchanged; it used to wrap them in `Case`.
- `scope.Each` with a limit below 1 runs without a limit, as errgroup's
  negative limit; it ran one at a time.
- `scope.Timeout` keeps a task's own error; once the deadline had passed,
  it replaced any error with `*TimeoutError`.
- `scope.Fork` on a closing scope returns a fiber already interrupted; it
  panicked, crashing a fiber that forks until its context ends.
- `scope.Main` exits with status 0 when the signal's cancellation stops
  the program, lets a second signal kill it, and takes `Run`'s options.
- `schedule.Repeat` treats a call that ctx's end interrupts as a stop, not
  a failure; `While` no longer stops a `Repeat`.
- `trace.End` records a cancellation as an "interrupted" event, not as the
  span's error.

Fixes:
- `check` and `else` lost the user's `err` when one was in scope:
  `x, err := f(); y := check g()` overwrote it.
- A `match` on an error with only sentinel arms used `==`, not `errors.Is`.
- `if`, `match` and `??` expressions after `&&`/`||` ran even when the
  left side short-circuited, and ran before operands to their left.
- A `match` arm's bindings hid the user's variables from later arms.
- `x = u?.Name` kept `x`'s old value when `u` was nil.
- `%` was doubled in strings with nothing interpolated; an error set whose
  cases have no fields imported `fmt` without using it.
- `p?.Score ?? 0` failed for a float64 field; a typed lambda passed to a
  generic function got an `any` result; a local variable named `fmt` or
  `errors` hid the package from generated code.
- Panics in one-line effect bodies reported the wrong line.
- The compiler crashed on `for f"…"` conditions and on lambdas in literals
  of undefined types; `??` in an `if` or `switch` header gives a clear
  error.
- The parser looped forever on a file ending inside `effect(`, freezing
  `ego fmt` and the language server; `effect` literals taking `struct{}`
  didn't parse.
- `scope`: a panicking release skipped the others; `Defer` and `Acquire`
  after the scope closed leaked; a scope kept every finished fiber; `Race`
  cancelled from outside reported the cancellation once per task.
- `schedule`: `Jittered`, `While`, `UpTo`, `Tap`, `Min` and `Max` dropped
  `Delayed`; jitter near the largest delay overflowed on amd64.
- `trace.LogHandler` put the IDs inside `WithGroup` groups.
- `layer`: a provider bound through interfaces ran once per interface;
  `layer.Close` on a type without `Close` crashed `ego generate`.
- `ego fmt -l ./...` checked nothing; `ego test` took flag values for
  packages; the language server crashed on a negative `Content-Length`
  and could map positions with an outdated source map.
- `ego vet`'s exhaustive check flagged switches on durations and bit
  flags; ctxcheck checks `TryGo`, fiberjoin `var f = scope.Fork(…)`.

## v0.2.2 (2026-10-07)

**To update:** run `ego generate ./...`; only `layers_ego.go` files may
change (import order, variable names).

- `layer.Build` can use a `layer.Set` declared in another package, such as
  a framework's.
- `ego generate ./...` finishes in one run on a module whose packages
  import each other's `.ego` code before any of it is generated, even when
  an external test imports a package that imports its own. It used to need
  several runs, or never finished.
- An external test can import a package that imports the package under test
  and pass values between them, as `go test` allows; `ego generate` failed
  with errors like `cannot use cfg (config.Config) as config.Config`.
- Generated wiring (`layers_ego.go`) lists the standard library's imports
  first, and its variables no longer hide package names.

## v0.2.1 (2026-10-04)

**To update:** run `ego generate ./...` in each module; the generated code
changes, mostly its //line directives.

- Comments on error-set cases and enum members reach the generated Go: a
  comment above a case documents its type or constant, and one after it
  stays at the end of its line. They were dropped.
- Generated code reports the right lines for a documented declaration that
  follows code lowering made longer (an error set, say): panics and the
  debugger pointed at the doc comment, and the lines below by as much.

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
