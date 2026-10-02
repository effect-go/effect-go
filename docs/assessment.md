# EffectGo: assessment and direction

*2026-10-01. Baseline: Go 1.27.1 and Effect 4.0. This document replaces the original handoff brief as the working direction.*

## TL;DR

- **The dialect's goal is readability.** Every addition is sugar that removes noise from the source. Where that conflicts with Go's habit of making things explicit (implicit `ctx` in `effect` functions, for example), the dialect chooses less noise and says so.
- **Port what Effect guarantees, not how it works inside.** Effect's fiber runtime, lazy values and TestClock exist because JavaScript has no threads, no cancellation and eager Promises. Go already has goroutines, `context`, `defer` and `testing/synctest`.
- **Lower everything to direct-style Go**: the code you would write by hand (`if err != nil`, explicit `ctx`, goroutines). Never lower to `FlatMap` chains. Effect v4 itself recommends `Effect.gen`/`Effect.fn` over chains of combinators.
- **Build only what Go has refused**: error-handling syntax, closed error sets and sum types, nil-safe access, short anonymous functions, `match` and `if` as expressions, and string interpolation. Go is shipping the rest itself.
- **Keep these from Effect v4**:
  - services and layers, as dependency wiring checked at compile time, with lifecycles;
  - reason errors;
  - structured concurrency with a Cause model;
  - schedules;
  - scoped resources;
  - `Effect.fn`-style functions, declared with `effect` instead of `func`: a span per call, with `ctx` passed implicitly.
- **The main cost is tooling** (a gopls proxy), not the runtime.

## 1. What Go developers complain about

| Rank | Complaint | Evidence |
|---|---|---|
| 1 | Error-handling verbosity | The top complaint for a decade. The Go team stopped pursuing error syntax in June 2025 ([blog](https://go.dev/blog/error-syntax)), and new proposals are closed the same day they're filed (Feb and Jun 2026). The team's own `?` proposal got 236👍/500👎 ([golang/go#71203](https://github.com/golang/go/issues/71203)). |
| 2 | No sum types, enums or exhaustive switch | [golang/go#19412](https://github.com/golang/go/issues/19412) has 1,043👍, the most of any language proposal after generics. A union proposal was declined in Sep 2026 ([golang/go#80607](https://github.com/golang/go/issues/80607)). |
| 3 | Nil safety | Recurs in the free-text answers of the [2025 survey](https://go.dev/blog/survey2025). |
| 4 | Verbose closures | [golang/go#21498](https://github.com/golang/go/issues/21498) has 674👍/407👎. Robert Griesemer (Go team), May 2026: "Nobody is blocked by this". |
| 5 | `context.Context` in every signature | ["Context should go away for Go 2"](https://faiface.github.io/post/context-should-go-away-go2/), reposted to HN in 2025. |

The other side:
- 91% of respondents were satisfied in the 2025 survey.
- "Boring is good" is a core Go value.
- The Go team's case against `?`: an explicit `if` gives you a line to put a breakpoint on, there should be one way to do things, and LLMs write the boilerplate anyway.
- Teams' top challenge in the [2024 H2 survey](https://go.dev/blog/survey2024-h2-results) was keeping a consistent coding style (58%), and a dialect makes that harder.

What Go has refused versus what it is shipping:

| Go has refused (room for a dialect) | Go is shipping (don't build it) |
|---|---|
| Error-handling syntax | Generic methods on concrete types (1.27) |
| Sum types and closed unions | `errors.AsType`, `new(expr)` (1.26) |
| Short anonymous functions (stalled) | Inferred composite literals (accepted for 1.28) |
| String interpolation ([declined](https://github.com/golang/go/issues/34174)) | `testing/synctest` (1.25), goroutine-leak profile (1.27) |
| Conditional expressions (the Go FAQ allows only one conditional construct) | |

## 2. Effect v4 idioms mapped to Go

Read first-hand from [LLMS.md](https://github.com/Effect-TS/effect/blob/main/LLMS.md) and its `ai-docs` examples.

| Effect v4 idiom | Go equivalent | Verdict |
|---|---|---|
| `Effect.gen` for inline code | Ordinary sequential Go | Comes free; Go is already direct style |
| `Effect.fn("name")` adds a span and stack frames; `fnUntraced` for hot paths | Functions declared with `effect` instead of `func`: an implicit `ctx` parameter, plus OpenTelemetry span start/end with error recording; an ordinary function otherwise | **Keep, as a core feature** |
| `Context.Service` with a `static layer`: the constructor effect `yield*`s its dependencies, and methods are `Effect<A, E>` with no requirements | A struct holding its dependencies, plus a constructor | **Keep.** Go's idiom already matches. |
| `Layer.provide` / `provideMerge`: unsatisfied requirements are type errors, each layer is built once and torn down in order | Generated wiring (wire-style), checked at compile time, with finalizers on an app `Scope` | **Keep, as code generation** (§4.3) |
| `Effect.acquireRelease` inside a layer | `scope.Acquire(open, close)`, released last-in first-out | **Keep** |
| `Schema.TaggedError` with `catchTag`/`catchTags` | Error types plus `errors.AsType` (1.26) | Keep |
| Reason errors: `AiError{ reason: RateLimit \| Quota \| Safety }`, handled with `catchReason`/`catchReasons`/`unwrapReason` | One sealed error interface per service, with the reasons as its variants, matched exhaustively | **Keep. This is the typed-error model.** |
| Wrapping per service: `Effect.mapError(reason => new MailerError({ reason }))` | Go's `%w` wrapping, but typed and closed | **Keep**, with `check … as Reason` sugar |
| Repository `findById` returns `Effect<Option<A>, RepoError>` | `FindByID(ctx, id) (User, bool, error)`; the service decides `NotFound` | **Keep**, using comma-ok rather than an `Option` struct |
| `Schedule`: `exponential`, `spaced`, `recurs`, `min` (continue while any continues, fastest delay), `max` (continue while all continue, slowest delay), `jittered`, `while`, `tap`; used with `Effect.retry` and `orDie` | A `schedule` package with the same algebra over `func(ctx) (T, error)` | **Keep.** Go has nothing like it. |
| Fibers supervised by their parent; interruption | Goroutines plus a `Scope`; cancellation is cooperative through `ctx` | Adapt |
| `Exit` / `Cause` | A `Cause` that implements `error`: Fail, Die or Interrupt | Keep, but Die re-panics by default (§3) |
| `Clock`, TestClock, `DateTime` | `testing/synctest` | Drop |
| `ManagedRuntime`, the bridge to non-Effect code | Not needed: generated code is plain Go | Drop |
| `Layer.launch` | `app.Run(ctx, graph)` with signal handling | Later |
| `LayerMap` (resources keyed by e.g. tenant) | A keyed resource cache on a scope | Later |
| `RequestResolver` batching, `Stream`, `PubSub`, `Schema` | dataloader, `iter.Seq` and channels, json v2 plus validators | Out of scope |

## 3. What changes from the original brief

1. **No `Effect[A]` programming model.**
   - Effectful code is `func(ctx, …) (T, error)`.
   - A lazy `func(ctx) (T, error)` appears only as the argument of `retry`, `timeout`, `race`, `fork` and `all`.
   - An instruction-tree interpreter only pays off for durable replay or request batching. For that reason Temporal [bans goroutines in workflow code](https://docs.temporal.io/develop/go/best-practices/multithreading).
2. **No `FlatMap` lowering.**
   - Without a full state-machine transform it breaks on loops, `defer`, `break`/`continue`, `select` and early returns.
   - Every continuation needs explicit types, so a type checker is needed anyway.
   - Stack traces turn into `func1.func2.func3`.
   - It costs 19× (§5).
3. **`effect!` becomes a prefix `check`**, with automatic error labels, optional custom ones and `as` for reasons, plus `else`, `must` and a `fail` statement. Error labels are there from day one. `?` is kept for nil-safe access (`?.` and `??`), the meaning it has in TypeScript.
4. **No custom scheduler and no fiber runtime.** `Fiber[T]` is a typed handle over a goroutine and `context.WithCancelCause`.
5. **Interruption is cooperative**, because Go [declined killable goroutines](https://github.com/golang/go/issues/50678). Interrupting a fiber means:
   - cancel its context with a cause;
   - wait for it to stop, with a deadline;
   - run its finalizers;
   - report any fiber that doesn't stop.
6. **Panics (Die) re-panic by default.**
   - errgroup shipped panic propagation in Apr 2025 and [reverted it](https://github.com/golang/sync/commit/7fad2c9213e0821bd78435a9c106806f2fc383f1) in Jun 2025: crash tooling lost stack traces, crashes were delayed, and it risked deadlocks.
   - Converting a panic into a value is opt-in, at boundaries (an HTTP handler returning 500).
7. **No TestClock.** Use `testing/synctest`.
8. **Stack safety is not an issue.** A 1,000,000-deep `FlatMap` chain ran in about 100 ms.
9. **No `throws` clause on every function.** Use v4-style reason errors: one closed error set per service, wrapping the errors of lower layers.
   - Swift's typed-throws proposal keeps untyped throws as the default ([SE-0413](https://github.com/swiftlang/swift-evolution/blob/main/proposals/0413-typed-throws.md)).
   - Java's checked exceptions showed the versioning cost ([Hejlsberg](https://www.artima.com/articles/the-trouble-with-checked-exceptions)).
   - Reason errors limit that cost: callers that only check the error type don't break when a reason is added.
10. **Services yes, per-function `requires` no.**
    - v4 puts behaviour in services whose layers resolve their dependencies, so service methods carry no requirements.
    - Tracking requirements per function only matters at the program's entry point, and the layer graph already covers that.
    - In Go, behaviour lives on a struct (`func (s *UserService) Get(...)`) whose fields are its dependencies.
11. **The repository returns absence as a value.**
    - v4's example repository returns `Option`, and `NotFound` is the service's decision.
    - In Go, the repository returns `(User, bool, error)` and the service returns a `NotFound` reason.
    - The brief put `NotFound` in the repository's failure channel instead.
12. **Tooling is the real budget** (§6).

## 4. Proposed architecture

Three layers, each useful on its own.

### 4.1 Runtime library (plain Go, targeting 1.26)

A sketch, not a final API:

```go
package scope

type Task[T any] = func(context.Context) (T, error)

// Run cancels and awaits all children and runs finalizers last-in first-out before returning.
func Run[T any](ctx context.Context, body func(s *Scope) (T, error)) (T, error)
func Fork[T any](s *Scope, task Task[T]) *Fiber[T]
func (f *Fiber[T]) Join() (T, error)
func (f *Fiber[T]) Interrupt(cause error)
func All2[A, B any](ctx context.Context, a Task[A], b Task[B]) (A, B, error) // first failure cancels siblings
func Race[T any](ctx context.Context, tasks ...Task[T]) (T, error)           // losers cancelled and awaited
func Acquire[T any](s *Scope, open Task[T], release func(context.Context, T) error) (T, error)
```

```go
package schedule

type Schedule struct{ /* pure value: decides whether to continue, and the next delay */ }

func Exponential(base time.Duration) Schedule
func Spaced(d time.Duration) Schedule
func Recurs(n int) Schedule
func Min(s ...Schedule) Schedule // continue while any continues; fastest delay
func Max(s ...Schedule) Schedule // continue while all continue; slowest delay
func (s Schedule) Jittered() Schedule
func (s Schedule) While(retryable func(error) bool) Schedule

// Retry uses time.Timer, so tests can run it under synctest.
func Retry[T any](ctx context.Context, s Schedule, task scope.Task[T]) (T, error)
```

**As built in week 1:** the Cause model is `scope.KindOf(err)`, which returns Fail, Die or Interrupt, plus a `*scope.Panic` that carries the original stack. Parallel failures are combined with `errors.Join`, so `errors.Is` and `errors.As` see every one of them. Fibers carry pprof labels, so goroutine dumps and the leak profile show which fiber leaked.

### 4.2 Analyzers (go/analysis)

These run on plain Go and need no dialect:
- Exhaustive `match` or type switch over a sealed error interface (prior art: [go-sumtype](https://github.com/BurntSushi/go-sumtype)).
- A child task that uses the parent's `ctx` instead of its own (the classic errgroup `ctx` vs `gctx` bug).
- A `Fiber` that is never joined.

### 4.3 Layers: dependency wiring checked at compile time, with lifecycles

**Effect v4:**
- `layerNoDeps` has type `Layer<UserRepository, never, SqlClient>`.
- `layerNoDeps.pipe(Layer.provide(SqlClientLayer))` exposes only `UserRepository`; `provideMerge` exposes both services.
- A missing dependency is a type error.
- Each layer is built once.
- Resources acquired with `acquireRelease` are released when the layer is torn down.

**Go today:**
- **Wiring by hand in `main`:** checked at compile time, but it doesn't scale, and lifecycles rely on `defer`.
- **fx and dig:** have lifecycle hooks, but resolve dependencies by reflection at startup. That is the same trade-off NestJS and tsyringe make in TS.
- **wire:** compile-time, with cleanup functions, but [archived](https://github.com/google/wire).

**Proposal:** providers are ordinary constructors. The tool resolves the graph at build time and generates plain wiring code that registers finalizers on an app `Scope`:

```go
// providers: ordinary Go
func NewPostgres(ctx context.Context, cfg Config) (*sql.DB, error)
func NewUserRepository(db *sql.DB) *UserRepository
func NewUserService(repo *UserRepository) *UserService

// generated
func BuildUserService(ctx context.Context, s *scope.Scope, cfg Config) (*UserService, error) {
	db, err := scope.Acquire(s,
		func(ctx context.Context) (*sql.DB, error) { return NewPostgres(ctx, cfg) },
		func(_ context.Context, db *sql.DB) error { return db.Close() })
	if err != nil {
		return nil, err
	}
	repo := NewUserRepository(db)
	return NewUserService(repo), nil
}
```

A test graph swaps one provider (for example, an in-memory repository), the way v4 tests swap layers. This can be built in plain Go regardless of what happens to the dialect.

### 4.4 Dialect (deliberately small)

*This section is the design as proposed on 2026-10-01. The built language is described in [AGENTS.md](../AGENTS.md), and the differences in [milestones-2-7.md](milestones-2-7.md).*

Every addition answers something Go has refused, lowers to the Go you'd write by hand, and keeps the grammar parseable without type information. Additions marked **v1** need types from go/types to lower, so they ship after the tooling gate (§9).

**Errors.** `?` is not used for errors. It means "might be missing", as in TypeScript (see below).

| You write | Meaning | Lowers to |
|---|---|---|
| `x := check s.carts.Load(id)` | if the call fails, return its error, labelled with the call | `x, err := s.carts.Load(ctx, id)`, then `if err != nil { return …, fmt.Errorf("carts.Load: %w", err) }` |
| `x := check s.carts.Load(id) "load cart {id}"` | the same, with your own label | `… return …, fmt.Errorf("load cart %v: %w", id, err)` |
| `x := check f() as Storage` | return it as a case of this function's error set | `… return …, Storage{Cause: err}` |
| `x := f() else fallback` | on failure, use `fallback` and carry on | `… if err != nil { x = fallback }` |
| `x := must f()` | panic: this should never fail | `… if err != nil { panic(err) }` |
| `fail NotFound{ID: id}` | return this error | `return User{}, NotFound{ID: id}`, with the empty values filled in |

- **`check` comes from the Go team's own 2018 draft design**, without its `handle` blocks. It plays the role of `yield*` in `Effect.gen`: every call that can fail is marked where it starts.
- **Labels are automatic.** Without one, `check` labels the error with the call it wraps: the last two parts of its name (`carts.Load`, `os.ReadFile`). For `retry` and `timeout`, the label comes from the inner call. `all` and `race` pass on the failing branch's error unchanged.
- **A custom label is a string right after the call.** Its `{…}` interpolates without an `f`, because a label has no plain-Go meaning to protect. The same goes for `fail "no user {id}"`.
- **`as` is only for error-set cases**, as in `check f() as Storage`.
- **`check` and `must` only start a statement or the right side of `:=`**, so every early return is visible at the start of its line.
- **Why not `yield`:** since Go 1.23 every iterator receives a callback named `yield`, and `yield(v)` sends a value out. Reusing the word for "take the value in, or return the error" would read backwards to Go developers.
- **`must` follows Go's `Must` naming** (`regexp.MustCompile`, `template.Must`). `else` and `must` handle the error locally, so they also work in functions that don't return one.
- **`check(err)` with parentheses stays an ordinary call**, so existing helpers named `check` or `must` keep working.

**Error sets, `enum` and `match`.**
- **`error` sets** are a closed set of reasons per service. They become a sealed interface plus one type per variant, each with `Error()`/`Unwrap()`.
- **`enum`** gives general sum types with the same lowering. An enum whose cases carry no data lowers to Go's usual `iota` constants instead.
- **`match`** is exhaustive over an error set, an `enum`, or a Go `const` block of a named type (Go's usual enum pattern). Other values need a `_` case.
- **On errors, `match` lowers to an `errors.AsType` chain,** so `%w` wrapping still matches. `errors.AsType[UserError](err)` catches any reason, like catching `AiError` as a whole in v4.

**`match` and `if` as expressions (v1).** Go has no conditional expression, so choosing a value takes a `var` plus a `switch` or an `if`.

```go
status := match err {
    nil         => http.StatusOK
    NotFound(_) => http.StatusNotFound
    Storage(_)  => http.StatusServiceUnavailable
}
fee := if user.Premium { 0 } else { 5 }
```

lowers to

```go
var status int
if err == nil {
	status = http.StatusOK
} else if _, ok := errors.AsType[NotFound](err); ok {
	status = http.StatusNotFound
} else if _, ok := errors.AsType[Storage](err); ok {
	status = http.StatusServiceUnavailable
}
var fee int
if user.Premium {
	fee = 0
} else {
	fee = 5
}
```

- An `if` expression needs an `else`, and each branch is a single expression.
- All arms must have one type, read from go/types.
- **Inside a larger expression, the value goes into a temporary declared just before the statement.** Operands to its left that have side effects move with it, so evaluation order doesn't change.

**Short anonymous functions (v1).**

```go
slices.SortFunc(users, (a, b) => cmp.Compare(a.Age, b.Age))
active := slices.DeleteFunc(users, u => !u.Active)
```

lowers to

```go
slices.SortFunc(users, func(a, b User) int { return cmp.Compare(a.Age, b.Age) })
active := slices.DeleteFunc(users, func(u User) bool { return !u.Active })
```

- **Types come from where the function is used:** the parameter it's passed to, the variable it's assigned to, or the return type.
- **With nothing to infer from,** write the parameter types: `(x int) => x * 2`.
- An expression body returns its value; a `{ … }` body is an ordinary function body.
- **Parsing needs no types; only lowering does.** Go's own proposal ([golang/go#21498](https://github.com/golang/go/issues/21498)) has no agreed syntax, and a Go team member argued against one variant because parsing it would need type information. The `=>` token avoids that.

**String interpolation (v1).**

```go
msg := f"user {u.Name} has {len(orders)} orders"
line := f"{amount:.2f} {currency}"
```

lowers to

```go
msg := fmt.Sprintf("user %s has %d orders", u.Name, len(orders))
line := fmt.Sprintf("%.2f %s", amount, currency)
```

- **Only `f"…"` strings interpolate,** so plain Go strings (JSON with braces, for example) keep their meaning. Go declined interpolation in the language itself ([golang/go#34174](https://github.com/golang/go/issues/34174)).
- **Literal braces and percent signs:** `{{` and `}}` write literal braces, and a `%` in the text becomes `%%`.
- **Format verbs:** the verb comes from the value's type (`%s`, `%d`, `%v`), and `{x:spec}` passes a format spec through.
- **It works anywhere a string does.** `check` labels and `fail` messages interpolate even without the `f`.

**Missing values: `?.` and `??` (v1).**

```go
city := u?.Address?.City ?? "unknown"
port := os.LookupEnv("PORT") ?? "8080"
```

lowers to

```go
city := "unknown"
if u != nil && u.Address != nil {
	city = u.Address.City
}
port, ok := os.LookupEnv("PORT")
if !ok {
	port = "8080"
}
```

- **`?.` stops at the first nil** and gives the zero value of the final type.
- **`??` applies when the left side is absent:** nil, a missing map key, a failed type assertion, or a function that returns a value plus a found flag.
- **`??` never applies to errors.** Those go through `check`, `else` and `must`.
- **Typed nil isn't caught.** An interface holding a nil pointer isn't nil in Go, so `?.` doesn't stop there either.

**Concurrency and tracing.**
- **`all` / `race` / `retry` / `timeout`** take their arguments lazily. Each argument becomes a task, with `ctx` rebound to the child's context.
- **They are predeclared names,** like Go's `min` and `max`, so your own `retry` or `all` takes precedence.
- **`effect`** declares a function the way `func` does, and is the equivalent of `Effect.fn`: `effect (s *Shop) Checkout(id CartID) (Receipt, error)`.
  - **Implicit `ctx`.** The generated function takes `ctx context.Context` as its first parameter, but the source doesn't declare it and calls don't pass it.
  - **Calls get `ctx` for you.** Inside an effect function, a call whose callee's first parameter is a `context.Context` and that leaves it out receives the current `ctx`. This works for plain Go APIs too (`db.QueryContext(q)`), and reads the signatures from go/types.
  - **Branches get their own.** Inside `all`, `race`, `retry` and `timeout`, the `ctx` passed is the branch's, so the "wrong context" bug can't be written.
  - **`ctx` is still in scope** for code that wants it explicitly, and an explicit argument is never replaced.
  - **One span per call**, named after the function, with its error recorded.
  - **From plain Go, it's an ordinary function** that takes `ctx` first. Interface methods are marked the same way: `effect Get(id UserID) (User, error)`.
  - **`effect` replaces `func` rather than prefixing it**, so it reads as its own kind of declaration. Searching for `func Checkout` no longer finds these functions; search for `Checkout(` instead.
  - **Panics still re-panic** (§3). An effect function doesn't turn panics into errors; only branches of `all`/`race` bring them back to the caller.

Lowering rules:
- **Each source statement becomes one contiguous block of Go, in the same order.** Expression forms put their temporaries at the start of that block. This keeps source maps simple; Dingo's own notes call reverse mapping "fundamentally broken" once one line expands into many.
- **Output is committed** and gofmt'd, starts with `// Code generated … DO NOT EDIT.`, and carries `//line` directives.
- **Generated signatures return `error`**, so they work with every `func() (T, error)` API. The closed set is expressed as a sealed interface plus a doc comment.
- **The parser accepts every valid Go file unchanged.** Each addition starts with something that isn't valid Go at that spot (`check x`, `must x`, `as`, `else` after an expression, `=>`, `f"`, `?.`, `??`, `match`, `effect Name(`, `error Name {`). Predeclared names (`all`, `race`, `retry`, `timeout`, `fail`) give way to your own declarations.
- **The grammar never needs types; lowering does.** Anonymous functions, expression temporaries, f-string verbs, `?.` chains and the closures inside `all` read their types from go/types.

Example (the syntax is illustrative only):

```go
// users.ego
error UserError {
    NotFound{ ID UserID }
    Storage{ Cause error }
}

effect (s *UserService) Get(id UserID) (User, UserError) {
    u, ok := check s.repo.FindByID(id) as Storage
    if !ok {
        fail NotFound{ID: id}
    }
    return u, nil
}

func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
    u, err := h.users.Get(r.Context(), userID(r))
    match err {
        nil         => writeJSON(w, u)
        NotFound(e) => http.Error(w, f"no user {e.ID}", http.StatusNotFound)
        Storage(_)  => http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
    } // adding a reason to UserError makes this fail to compile
}
```

Generated (abridged):

```go
// UserError is the closed error set of UserService.Get: NotFound | Storage.
type UserError interface {
	error
	isUserError()
}

type NotFound struct{ ID UserID }
type Storage struct{ Cause error }

func (NotFound) isUserError()     {}
func (Storage) isUserError()      {}
func (e Storage) Unwrap() error { return e.Cause }

// Error() methods omitted.

func (s *UserService) Get(ctx context.Context, id UserID) (_ User, err error) {
	ctx, span := trace.Start(ctx, "UserService.Get")
	defer trace.End(span, &err) // also records a panic, then lets it continue
	u, ok, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return User{}, Storage{Cause: err}
	}
	if !ok {
		return User{}, NotFound{ID: id}
	}
	return u, nil
}
```

Lazy arguments:

```go
// source
user, orders := check all(s.users.Get(id), s.orders.ForUser(id)) "load dashboard"

// generated: each closure takes its own ctx, and the calls inside it receive that one
user, orders, err := scope.All2(ctx,
	func(ctx context.Context) (User, error) { return s.users.Get(ctx, id) },
	func(ctx context.Context) ([]Order, error) { return s.orders.ForUser(ctx, id) },
)
if err != nil {
	return Dashboard{}, fmt.Errorf("load dashboard: %w", err)
}
```

## 5. Benchmarks

The code is in [`experiments/runtime-bench`](../experiments/runtime-bench); the raw results are in `results-m4pro-go1.27.1.txt`. Setup:
- Go 1.27.1 on an Apple M4 Pro; median of 5 runs.
- Ten sequential steps, each a non-inlined call that can fail, passing a 32-byte struct.

| Variant | ns/op | allocs | vs plain Go |
|---|---|---|---|
| Plain Go (same as direct-style output) | 16 | 0 | 1× |
| Plain Go with a context check each step | 27 | 0 | 1.7× |
| Panic-based `Try`/`Gen`, no compiler | 16 | 0 | 1× (160 ns when a step fails) |
| Closures, the brief's nested-`FlatMap` lowering | 312 | 31 | 19× |
| Closures, chain built once and reused | 112 | 10 | 7× |
| Minimal interpreter (best case) | 664 | 82 | 41× |

| Concurrency operation | Cost |
|---|---|
| Raw `go` + channel | 268 ns |
| `Fork`/`Join` with context and panic capture | 423 ns |
| `All3` with sibling cancellation | 1.2 µs |
| Panic turned into a Die, with stack | 3.4 µs |

A 1,000,000-deep chain runs in 70–110 ms with no stack overflow, for both closures and the interpreter.

Takeaways:
- **The interpreter is out, but not for speed.** These costs are small next to I/O; it loses because it buys nothing goroutines don't already give.
- **Panic-based `Try`/`Gen` costs nothing when no step fails**, which makes it fine for prototyping. Go culture treats panic-based control flow as a smell, so don't ship it.
- **fp-go's own benchmarks agree:** one `Map` through `Pipe1` costs about 200 ns and 9 allocations, against 24 ns and 1 allocation for a direct call.

To reproduce:

```bash
cd experiments/runtime-bench && go test -bench . -benchmem -count=5
```

## 6. Tooling facts

- **gopls ignores `//line`** ([golang/go#55043](https://github.com/golang/go/issues/55043)). A gopls proxy with source maps in both directions is therefore required; templ and Dingo both have one.
- **`//line` fixes panics and debugging but breaks coverage.** It makes panics, `runtime.Caller`, DWARF and Delve point at the original source. It breaks coverage ([golang/go#41222](https://github.com/golang/go/issues/41222)), and confuses pprof when a function switches files ([golang/go#56135](https://github.com/golang/go/issues/56135)).
- **Generated code has to be committed.** `-overlay` can't replace files under `GOMODCACHE`, so dependencies must ship real `.go` files.
- **Build-time rewriting has costs.** Rewriting with `-toolexec` (as Orchestrion does) works, but fights the build cache and confuses Delve ([golang/go#69887](https://github.com/golang/go/issues/69887)).
- **[templ](https://github.com/a-h/templ) is the model to copy:**
  - narrow scope;
  - readable, committed Go output;
  - an error-tolerant parser;
  - a gopls proxy;
  - `templ fmt` and a watch mode;
  - the tool version pinned with `go tool`.
- **Keep the dialect's grammar context-free.** Go must be parseable without a symbol table, and the dialect should be too.
- **`.ego` files need their own formatter, `ego fmt`.** gofmt rejects the new syntax, so standard gofmt only formats the generated Go.
  - The compiler already needs its own copies of `go/scanner`, `go/parser` and `go/ast`. The formatter adds a copy of `go/printer` with print rules for the new syntax, so it reuses the parser work.
  - Go+ (now XGo) maintains the same set of copies and ships `gop fmt`. `templ fmt` formats templ's own syntax and hands embedded Go to `go/format`.
  - The ongoing cost is merging upstream changes when Go adds syntax: generic methods in 1.27, inferred composite literals in 1.28.
  - Don't format by rewriting the new syntax into placeholder Go, running gofmt and rewriting back. The placeholders have different widths, so gofmt's column alignment comes out wrong.
  - The gopls proxy routes format-on-save to `ego fmt`. CI runs `ego fmt -l` on `.ego` files, and `gofmt -l` and `go vet` on the generated Go.

## 7. Prior art

| Project | What it is | Status (2026-10) |
|---|---|---|
| [Dingo](https://github.com/MadAppGang/dingo) | `?` with `Result` types, enums, match, lambdas, `?.`/`??`, gopls proxy, VS Code extension, agent docs | 1.9k★, Apache 2.0, started Nov 2025. One maintainer (384 of 387 commits). Fast until Jan 2026 (v0.3→v0.9), then bursts in Mar, Jul (v0.14) and Sep 2026; issues from Jul–Aug unanswered. Missed its 1.0 target |
| [Lisette](https://github.com/ivov/lisette) | New language compiling to Go, with its own type inference and LSP | 1.5k★, active; Go code can't call it yet |
| [XGo](https://github.com/goplus/xgo) | Successor to Go+, with a gopls fork | 9.4k★, active |
| [Borgo](https://github.com/borgo-lang/borgo) | Rust-like language compiling to Go | 4.6k★; dead since 2024; no license |
| [templ](https://github.com/a-h/templ) | HTML DSL compiling to Go | 10.5k★; the success model |
| [fp-go v2](https://github.com/IBM/fp-go) | FP library; has had an `effect` package since Jan 2026 | 2k★; untyped errors; HN verdict on the library: ["no longer Go"](https://news.ycombinator.com/item?id=37171149) |
| [mbauer83/effect-golang](https://github.com/mbauer83/effect-golang) | `Effect[R,E,A]`, fibers, layers, TestClock | 2★, three weeks old: the brief's scope, and nobody uses it |
| [Ox](https://ox.softwaremill.com/latest/) (Scala) | Direct-style structured concurrency, retries and resources on virtual threads | 1.0 in Aug 2025; the closest model in spirit |
| errgroup, [conc](https://github.com/sourcegraph/conc) | Fork/join | errgroup returns only the first error, and its panic propagation was reverted; conc inactive since Jan 2024 |
| [failsafe-go](https://github.com/failsafe-go/failsafe-go), [backoff](https://github.com/cenkalti/backoff) | Retry policies | Policies stack, but there is no schedule algebra |
| [fx](https://pkg.go.dev/go.uber.org/fx), [wire](https://github.com/google/wire) | Dependency injection | fx: runtime reflection plus lifecycle hooks. wire: compile-time, archived |

What Dingo tells us (checked 2026-10-01):
- **There is demand.** Nearly 2,000 stars in under a year, with no company behind it.
- **A sugar-only dialect seems to stall.** After the launch spike it slowed to occasional maintenance and never gained a second regular contributor.
- **Our v1 shorthand overlaps with it.** Short functions, `match` expressions and `?.`/`??` are Dingo's territory. What only EffectGo has is the Effect part: `effect` functions with implicit `ctx`, structured concurrency, per-service error sets, schedules and checked wiring. That's what to lead with.
- **Reuse its code, don't depend on it.** It's Apache 2.0, so its parser and gopls proxy can be studied and borrowed with attribution. With a single maintainer, it isn't a foundation to build on.

## 8. Risks

- **Tooling budget**, mainly the gopls proxy, plus keeping the forked parser and printer in step with each Go release.
- **Go culture:** "boring is good", and dialects fragment a team's coding style. Implicit `ctx` hides a parameter that Go makes explicit on purpose.
- **LLMs know Go but not the dialect.** Keep it a strict superset of Go that one page of docs can teach.
- **Typed errors still carry a versioning cost.** Reason errors contain it but don't remove it.
- **Feature creep.** The dialect is now about a dozen additions. Each one costs editor support and a line in AGENTS.md, and the whole language still has to fit on one page.
- **Cancellation is cooperative**, so a fiber running code that ignores `ctx` cannot be interrupted.
- **The "Effect" name signals monads** to Go developers.

## 9. Milestones (about 7 weeks, each with a go/no-go gate)

> **Status (2026-10-02):** every step below is built, and every gate passes. The editor gate was tested with a scripted LSP client against gopls, not yet inside VS Code. Results and the changes made along the way are in [milestone-1.md](milestone-1.md) and [milestones-2-7.md](milestones-2-7.md).

How the plan is ordered:
- **Riskiest first.** Editor support decides whether a dialect gets used, so it's tested in week 2, before most of the syntax exists.
- **Library first.** The runtime ships as a plain Go library before the dialect. It's useful without new syntax, and its users become the dialect's first audience.
- **Lead with what only EffectGo does.** v0 is the Effect part. The everyday shorthand that overlaps with Dingo comes last.
- **Target Go 1.26,** the oldest supported release, for the runtime and the generated code. Companies often lag one version. Tests may use 1.27 tools such as the goroutine-leak profile.

0. **Before any code (1–2 days):**
   - Choose the name and the first audience. **Name decided: effect-go**, module `github.com/effect-go/effect-go`. The first audience is still open.
   - Read Dingo's parser and gopls proxy, and decide what to borrow.
1. **Week 1: runtime library** (`scope`, `Cause`, `schedule`, `Acquire`, `trace`), tested with `synctest` and the goroutine-leak profile. Write both demos (the repository service and the dashboard) in plain Go.
   - *Gate:* clearly better than errgroup + backoff on the dashboard demo.
   - Release it as a plain Go library, usable without the dialect.
2. **Week 2: parser and editor spike.**
   - The forked parser and `ego fmt`, on the same syntax tree.
   - `ego generate` lowering just `check` and `effect` with implicit `ctx`.
   - A minimal gopls proxy, using templ's architecture and whatever Dingo's proxy offers.
   - *Gate:* in VS Code, on a file using `check` and `effect`, go-to-definition, hover, diagnostics and format-on-save all work.
   - *Gate:* `ego fmt` leaves its own output unchanged, and formats every `.go` file in the standard library exactly as gofmt does.
   - If the editor gate fails: stop adding syntax, and ship the library, the analyzers and the layer code generation, all of which are plain Go.
3. **Weeks 3–4: the rest of dialect v0**: `else`, `must` and `fail`, error sets with exhaustive `match` statements, and `all`/`race`/`retry`/`timeout`. Add rename to the proxy.
   - *Gate:* the generated code passes your own code review, and the demos are at least 30% shorter.
   - *Gate:* an agent given only the one-page AGENTS.md writes correct EffectGo for both demos. That page ships with the release.
4. **Week 5: layer code generation**: the graph checked at compile time, finalizers on a scope, and a test graph that swaps one provider.
   - *Gate:* a missing provider is a build error with a readable message, and shutdown order is correct under `synctest`.
5. **Weeks 6–7: dialect v1**: short anonymous functions, `match` and `if` as expressions, string interpolation, and `?.`/`??`. They need type information from go/types.
   - *Gate:* every feature lowers to Go you'd accept in review, `ego fmt` and the proxy handle it, and the AGENTS.md cheat sheet still fits on one page.
   - *Gate:* the agent test from v0 still passes with the new features.

## 10. Open questions

*Answered while building weeks 2–7 (see [milestones-2-7.md](milestones-2-7.md)):*
- *signatures return `error`;*
- *implicit `ctx` reaches plain Go functions;*
- *the graph uses a wire-style marker;*
- *`=>`, with bare single parameters;*
- *one expression per `if` branch;*
- *f-strings take format specs.*

*Still open: anonymous `effect`, where panics may become values, unexported error-set inference, and the first audience.*

- Should generated signatures return `error` (interop) or the sealed type (precision)? Current choice: `error`.
- Should unexported functions declare their error sets or have them inferred?
- How is the dependency graph declared: a wire-style marker call (valid Go) or a dialect `layer` block?
- Should implicit `ctx` reach plain Go functions that take a `context.Context` (current choice), or only other effect functions?
- Is there an anonymous form of `effect` (for goroutines and callbacks), and how is it spelled?
- Anonymous functions: `=>` (TypeScript, C#) or `->` (Kotlin, Java)? And should a single parameter be allowed without parentheses (`u => !u.Active`)?
- Should an `if` expression allow statements before its final value, or stay one expression per branch?
- Should f-strings support format specs (`{amount:.2f}`) from the start, or only plain values?
- Where should code be allowed to turn panics into values?
- The first audience (the name is decided: effect-go).

## Sources

- **Effect v4:**
  - [LLMS.md](https://github.com/Effect-TS/effect/blob/main/LLMS.md)
  - [reason errors](https://github.com/Effect-TS/effect/blob/main/ai-docs/src/01_effect/04_errors/20_reason-errors.ts)
  - [layer composition](https://github.com/Effect-TS/effect/blob/main/ai-docs/src/01_effect/03_services/20_layer-composition.ts)
  - [acquireRelease in a layer](https://github.com/Effect-TS/effect/blob/main/ai-docs/src/01_effect/05_resources/10_acquire-release.ts)
  - [schedules](https://github.com/Effect-TS/effect/blob/main/ai-docs/src/06_schedule/10_schedules.ts)
  - [services migration](https://github.com/Effect-TS/effect-smol/blob/main/migration/services.md)
- **Go:**
  - [error syntax decision](https://go.dev/blog/error-syntax)
  - proposals for [short function literals](https://github.com/golang/go/issues/21498) and [string interpolation](https://github.com/golang/go/issues/34174)
  - [2025 survey](https://go.dev/blog/survey2025)
  - release notes for [1.25](https://go.dev/doc/go1.25), [1.26](https://go.dev/doc/go1.26) and [1.27](https://go.dev/doc/go1.27)
- **Direct style elsewhere:**
  - [Ox](https://ox.softwaremill.com/latest/)
  - [Arrow on suspend vs IO](https://arrow-kt.io/learn/design/suspend-io/)
  - [Effect vs Promise](https://effect.website/docs/additional-resources/effect-vs-promise/)
