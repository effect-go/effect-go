# effect-go in one page

`.ego` files are Go plus the additions below. `ego generate` compiles each `x.ego` to `x_ego.go` (plain Go, committed; never edit it), and `x_test.ego` to `x_ego_test.go`. `ego test` regenerates, then runs `go test` with the same arguments. `ego fmt -w .` formats `.ego` files. Every valid Go file is a valid `.ego` file. Import packages as usual, except the runtime packages `scope`, `schedule`, `trace` and `layer` (`github.com/effect-go/effect-go/…`), which are imported for you.

## Errors

| Write | Means |
|---|---|
| `x := check f(a)` | if `f` fails, return its error wrapped as `"f: <err>"` (the label is the last two parts of the callee: `repo.Find`) |
| `x := check f(a) "load {a}"` | same, as `"load <a>: <err>"`; `{expr}` interpolates |
| `x := check f(a) as Storage` | return the error as the case `Storage{Cause: err}` of this function's error set |
| `x := check f(a) as Invalid{Reason: "bad {a}"}` | the same, with fields of your own; the cause still goes in an error field if the case has one |
| `x := f(a) else fallback` | on error, use `fallback` |
| `x := must f(a)` | on error, panic |
| `check err` | return `err` if it isn't nil (for an error value you already have) |
| `fail NotFound{ID: id}` / `fail "bad {id}"` | return this error, with zero values for the other results |

`check` and `must` only start a statement or the right side of `=`/`:=`. `check f()` and `must f()` alone drop the other results. A function using `check` or `fail` must return an `error` (or an error set) last. Inside a function that returns an error set, every `check` needs `as Case`, unless the callee returns the same set.

## Error sets, enums, match

```go
error UserError {                  // sealed interface + one struct per case
    NotFound{ ID UserID } "no user {ID}" // optional Error() message, fields by name; default "not found (ID …)"
    Storage{ Cause error }        // an error field is the cause: Unwrap returns it
}
enum Shape { Circle{ R float64 }; Dot }  // with data: a sealed interface
enum Color { Red; Green }                // without: iota constants + String()

match err {                         // statement; on errors it uses errors.AsType
    nil         => ok(w)
    NotFound(e) => http.Error(w, f"no user {e.ID}", 404)  // e is the case value
    Storage(_)  => { log(err); http.Error(w, "retry", 503) }
}
code := match c { Red => 1; _ => 0 }   // expression form: every arm is one value
```

`match` must cover every case of an error set, enum or named-constant type, or have a `_` arm. On a plain `error`, the set is the one the cases belong to. Functions declared to return `(T, UserError)` compile to `(T, error)`; `fail` takes one of its cases. An error that matches no arm panics.

## effect functions and concurrency

```go
effect (s *Shop) Checkout(id CartID) (Receipt, error) {   // instead of func
    cart := check s.carts.Load(id)                          // ctx passed for you
    user, hold := check all(s.users.Get(cart.User), s.stock.Reserve(cart.Items))
    pay := check retry(s.policy, s.pay.Charge(user, cart.Total)) "charge {id}"
    img := race(s.cdn.Primary(k), s.cdn.Mirror(k)) else DefaultImage
    return Receipt{cart, hold, pay}, nil
}
```

- An `effect` function gets a hidden first parameter `ctx context.Context` and an OpenTelemetry span. Callers in plain Go pass `ctx` explicitly; calls inside effect functions omit it: any call whose callee takes a `context.Context` first and leaves it out gets `ctx`. `ctx` is still in scope if you need it. Don't declare a `ctx` parameter yourself.
- Interface methods: `effect Get(id ID) (User, error)` declares `Get(ctx context.Context, id ID)`. From plain Go: `u, err := svc.Get(r.Context(), id)`.
- To bound several steps with `timeout`, put them in their own effect function: `return timeout(2*time.Second, load(d, id))`.
- `all(a(), b())` runs calls in parallel (up to 4, any types; more must share a type) and returns all values; the first failure cancels the others, and the error joins every real failure. `race(a(), b())`: first success wins, losers are cancelled; if all fail, the error joins them all. `retry(policy, f())` with a `schedule.Schedule`. `timeout(d, f())` fails with an error matching `context.DeadlineExceeded`. Arguments are calls, run lazily with their own `ctx`; they nest: `all(a(), retry(p, b()))`. They need an effect function (or a `ctx` in scope). They return `(values…, error)`: use `check` (a label goes after the closing parenthesis), `else`, or `return timeout(…)`. Neither `all` nor `race` adds a label. A panic in a branch re-panics in the caller.
- Policies: `schedule.Exponential(100*time.Millisecond)` waits 100ms, 200ms, 400ms…; `schedule.Recurs(3)` allows at most 3 retries; `schedule.Max(a, b)` continues while both do, with the longer delay (so `Max(Exponential(d), Recurs(3))` is "back off, 3 times"); `.Jittered()` spreads delays ±20% (all of these are `Schedule` methods, usable anywhere, including a package-level `var`); `.While(func(error) bool)` stops on errors it rejects; `.UpTo(d)` caps each delay.

## Shorthand

| Write | Means |
|---|---|
| `u => u.Active`, `(a, b) => a < b`, `(x int) => x * 2` | function literal; types come from the parameter or variable it's passed to (`.While(err => …)`); write them when there's none; a `{ … }` body works too |
| `if late { "sorry" } else { "ok" }` | conditional value; `else` is required; each branch is one expression |
| `f"{n} items, {total:.2f} EUR"` | `fmt.Sprintf`; `{{`/`}}` for braces; verbs from the type, or after `:`; `{t.Format("2006-01-02")}` works |
| `u?.Address?.City` | stops at the first nil pointer, giving the zero value |
| `a ?? b` | `b` when `a` is nil, a missing map key, a failed type assertion, or a `(v, ok)` call returning `!ok`. Never for errors |

`?.` can't follow a call. These forms can't appear in `for`/`case` headers; assign them to a variable first.

## Wiring (plain Go)

```go
//go:build egolayers                       // injector file: never compiled
func BuildApp(ctx context.Context, s *scope.Scope, cfg Config) (*App, error) {
    panic(layer.Build(AppSet))               // or layer.Build(AppSet, NewMemRepo) to swap one
}
var AppSet = layer.Set(layer.Close(NewDB), NewRepo, NewService, NewApp)
```

Providers are ordinary constructors returning `T`, `(T, error)` or `(T, cleanup, error)`. `ego generate` writes `layers_ego.go`. Run the app inside `scope.Run(ctx, func(s *scope.Scope) (T, error) {…})`; finalizers run in reverse order when it returns.

## Runtime from plain Go

`scope.All2..All4`, `scope.Race`, `scope.Timeout`, `schedule.Retry`, `scope.Run`/`Fork`/`Acquire`, `trace.Start`/`trace.End(span, &err)`: the dialect lowers to exactly these.
