# Changelog

## Unreleased

**Fixes**
- A lambda whose parameter or result is a struct or interface type with
  fields, as `x => x.A == ""` over `[]struct{ A string }` items, gives
  generated Go that `gofmt -l` accepts. Formatting it again moved the
  body to its own lines.
- A lambda with a block body no longer gets blank lines at its start and
  end in the generated Go.

## v0.5.0 (2026-10-19)

To update, run `ego generate ./...`. It now also writes the Go of `.ego`
files that build constraints exclude on your platform, which
`ego generate -check` reports until then, and the generated code changes
where a branch kept its assignment's target.

**New**
- A function can return another package's error set:
  `func Get(id ID) (User, users.UserError)`. `check`, `fail`, `match` and
  callers in other packages treat it as they do this package's sets, and
  it compiles to `error`. Parameters can be declared as one too. An alias
  of a set (`type UserError = users.UserError`) in a signature is
  reported, naming the set to write instead.
- `ego generate` and `-check` compile the `.ego` files that build
  constraints exclude here, where their constraints hold: here with the
  tags they need, else on the first GOOS/GOARCH they allow. `x_linux.ego`
  is generated and type-checked from a Mac, and CI on one platform checks
  every platform's Go. Only files nothing satisfies, such as
  `//go:build ignore`, keep the note. `ego eject` compiles them the same
  way.
- `ego lsp`: go to definition, references and implementation open the
  `.ego` source even when the editor doesn't have that file open, in
  another package or the same one, instead of the generated `_ego.go`.
- `ego lsp`: an open `.ego` file excluded on this platform gets the
  compiler's and gopls's diagnostics, and saving writes its Go.

**Fixes**
- `x = p ?? x` keeps `x` when `p` is nil: it stored `p` before falling
  back on `x`, which then read the nil.
- A conditional value, `??` or `match` whose branch is the assignment's
  own target, as `c.Name = if b.Set { b.Value } else { c.Name }`, stores
  nothing in that branch, and `m = m ?? map[string]string{}` only tests
  `m`. Both lowered to `x = x`, which go vet reports as a
  self-assignment.
- `ego generate -tags` reaches the wiring: injectors can use providers
  that only those tags include.
- `ego lsp`: saving removes the Go left from `.ego` files deleted or
  renamed (case-only renames included), as `ego generate` does. Before,
  on a case-insensitive file system, the Go of a file renamed in case
  only was written under its old name.

## v0.4.0 (2026-10-16)

Fixes from a review of the whole codebase. To update, run
`ego generate ./...`: the generated code changes.

**Changes in behavior**
- `a ?? b` also falls back when `a` is a nil map value, a nil result of a
  type assertion or a nil `(v, ok)` result, as it does for a nil pointer.
  `u?.Tags["k"] ?? d` falls back on a missing key behind the chain too. To
  keep a nil that is present, use a comma-ok lookup instead.
- `scope.Main` logs and exits with status 1 when the shutdown fails (a
  resource fails to close, or fibers outlive `StopTimeout`), even after a
  clean stop by signal.
- `scope.KindOf` treats an error joining a cancellation and a real failure
  as a `Fail`: a nested `all` keeps the inner failure, and `trace.End`
  marks such a span as an error.
- `timeout(d, retry(p, f()))` keeps the last attempt's error (`errors.Is`
  matches it), and a task error that says more than the deadline is kept
  in the `*TimeoutError` ("timed out after 2s: load: context deadline
  exceeded"). `TimeoutError` has an unexported field now, so write
  `&TimeoutError{After: d}` with the field name.
- `fail (x)`, `match (x)` and `match *p` are the keywords, as `must (x)`
  already was. `ego fmt` no longer turns `fail (x)` into a call or
  `match *p` into a multiplication. Go that calls a function named `fail`
  or `match` with a space before `(` needs `fail(x)`, as gofmt writes it.

**Generated code**
- What `??`, `match` and the other forms need before a statement goes
  inside a lambda's body, an `each` function or a branch of `all`, `race`,
  `retry` and `timeout`. It ran once, outside, with the wrong variables and
  `ctx`.
- Operands are evaluated left to right when one of them needs statements
  before its own: in composite literals, return and assignment values, and
  a call's function, as call arguments already were. `x[f()] = a ?? b`
  evaluates its target once.
- Generated variables don't hide the user's: a `:=` with a check label, an
  else fallback or arms that mention a variable it declares, and `err` or
  `ok` from a select case, a lambda parameter or a match arm.
- `check err as Case` lets a case of the set through, as `check f() as Case`
  does.
- Effect functions: `n, err :=` after `n :=` compiles, and a parameter or
  receiver may be named `span`.
- Untyped constants in conditional values take the type they're used as:
  `if c { 1 } else { 0 }` returned as a `float64`, or multiplied by a
  `Duration`, was computed as an `int`.
- Labeled `for` and `switch` statements keep their label, so `continue`
  and `break` with it compile.
- `each(ids, n, s.Remove)` works when `Remove` returns only an error.
- A case message such as `"{Slug} moved {Times + 1} times"` can use its
  fields anywhere in an expression, and a raw message stays raw.
- `else` arms with only `_` don't import `errors`.
- A lambda passed to a generic function gets its parameter types from the
  other arguments: `Map(us, u => u.Name)` said it couldn't tell them.
- A lambda in an if or match branch takes its types from the variable it's
  assigned to.
- An effect literal called at once, `effect() (T, error) { … }()`, gets
  `ctx`.
- Stack traces name the line of a call in an if or match branch, not the
  closing brace's, and the right lines in a block-bodied lambda.

**Error messages**
- An undefined name that leaves a type unknown is reported, instead of
  only "can't tell the type of this expression" further down.
- `check` or `else` in a `for`, `switch` or type switch header, a lifted
  expression after an if's init statement, and one in a select case each
  get a message saying so, instead of unrelated errors such as
  `undefined: u`.
- A function declared to return another package's error set is an error
  saying so; it said check needs a function that returns an error.
- `return check … as Case` only says that check is misplaced.
- "must must start a statement" reads "must can only start a statement".

**Parser and formatter**
- `ego fmt -w` could replace a file with `package` when a backquoted string
  with `{…}` had many syntax errors: the parse gave up and reported none.
  Now it fails with the errors.
- `check * n` and `must & n`, multiplications and bitwise ands on variables
  named `check` and `must`, are Go again (v0.3.1 took them for the
  keywords). Only `must (x)` with a space is the keyword.
- `x := f() else { }` crashed `ego generate`; it is a syntax error.
- A match arm with nothing after `=>` before the next arm is empty, instead
  of taking the next arm as its body (`Red => Green => paint()`). In a
  match expression it's an error. A value can still start on the line
  after `=>`.
- `check f() as Case` in a `for` or `switch` header no longer swallows the
  body; the "check can only start a statement" message shows instead.
- Plain Go `import f"fmt"` and struct fields such as `X f"tag"` parse as Go
  again.
- Raw f-strings, raw check labels and raw fail messages work in files with
  CRLF line endings.
- `effect` before an embedded interface type is an error; `ego fmt` used to
  drop it.
- Clearer f-string errors: `f"{a)}"` points at the `)`, `f"{a b}"` names
  the `b`, and `f"\{ {a}"` reports only the bad escape.
- `ego fmt` keeps an if expression written over several lines on several
  lines, so a comment beside a value stays there. It prints `v ?? w else z`
  without parentheses, keeps `match x {}` as `{}`, and removes blank lines
  after `{` and before `}` in error sets and enums.

**Commands**
- `ego generate` never overwrites a hand-written `x_ego.go` or
  `layers_ego.go`: it reports it and goes on with other directories. A
  `layers.ego` next to injectors is reported too.
- `.ego` files follow Go's build rules: `x_linux.ego` and `//go:build`
  lines, with `-tags` for `ego generate` and `ego test`. The Go of a file
  excluded on this platform is checked for staleness.
- Go left from renamed `.ego` files, case-only renames included, is
  removed by `ego generate` and ignored by the language server.
- `ego generate` takes import paths, rejects missing directories and
  files, and skips `_dirs` in `./...`, as `go` does.
- `ego test` handles go test's flags without values (`-c`, `-artifacts`),
  `-C` and `-tags`, and restores the generated files on SIGTERM and SIGHUP
  during coverage runs.
- `ego eject -w` regenerates first, and ejects files excluded on this
  platform from their committed Go.
- `ego fmt` on a directory needs `-l` or `-w`, and keeps going after a
  failed write.
- `ego vet` passes the analyzers' flags, such as `-exhaustive`, only to the
  analyzers.
- `ego new` gets the runtime at ego's own version, not `@latest`.
- A UTF-8 byte order mark in a `.ego` file is ignored, and a file outside a
  module gets a clear error.

**Language server**
- Renames no longer corrupt `.go` files, and a rename that would change a
  `.ego` file that isn't open fails, naming it.
- Imports that completion adds go in the `.ego` file's imports.
- Paths with characters such as `@` work.
- Closing a directory's last `.ego` file releases all its buffers, and
  files changed on disk are followed.
- A bad request, or a crash in one, no longer ends the session.

**Runtime**
- `all`, `each` and `race` cancelled from outside with a cause of their own
  report it once, not once per task.
- `trace.End` records a task's panic (`*scope.Panic`) with its value and
  the task's own stack.
- `schedule.Exponential` no longer returns a negative delay at exactly
  2^63ns on amd64.
- `scope.Acquire` returns the error of releasing a resource that opened as
  the scope closed, and a panic in that release comes back as a
  `*scope.Panic`.

**Wiring and analyzers**
- `ego generate` writes layers wiring that compiles for packages named
  differently from their path (`redis/v9`, `yaml.v3`), two imported
  packages with the same name, `func(context.Context) error` cleanups,
  variadic providers, and generic instances in another package's set.
- A function literal or a call given as a provider is a clear error,
  instead of an internal one.
- Alias types count as the same type, a provider in two sets counts once,
  a provider passed to `layer.Build` replaces a set's provider of an
  interface its type implements, and a cleanup of a named func type is
  accepted.
- `ctxcheck` no longer reports `g, ctx := errgroup.WithContext(ctx)`.
- `exhaustive`: an interface case in a type switch covers the cases that
  implement it.

## v0.3.5 (2026-10-15)

- `ego generate` removes the Go it generated from a `.ego` file that was
  deleted or renamed (`x_ego.go`, `x_ego_test.go`), and `layers_ego.go` once
  no injector is left; it recognizes its files by their "Code generated by
  ego generate" line, so hand-written `*_ego.go` files stay. `-check` fails
  on such leftovers. Before, they kept compiling into the package. Reported
  by goncini.

## v0.3.4 (2026-10-13)

- No change in behavior or generated code. The compiler's fix for `??` in
  lambdas (v0.3.2) is replaced by a general one: a lambda's expression body
  is lowered only in the final round, like statements, so `match`,
  conditional values and the other forms there are typed the same way.

## v0.3.3 (2026-10-13)

- An effect function's span comes from its parent span's tracer provider when
  the parent is recording, and from the global provider otherwise. Before,
  every span came from the global provider, so with two providers in one
  process (two apps in parallel tests), spans could land in the wrong one.
  Suggested by goncini.

## v0.3.2 (2026-10-13)

- `??` works in a lambda whose parameter types come from the call, as in `strings.Map(r => swap[r] ?? r, s)`.

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
