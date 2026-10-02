# todo

A todo list on PostgreSQL, written in effect-go: a small but complete app
that uses most of the dialect and the runtime.

```bash
go build -o todo ./todo        # from examples/; the generated *_ego.go files are committed
export TODO_DATABASE_URL=postgres://localhost:5432/todo?sslmode=disable
./todo add buy milk -p high -due 2026-10-10
./todo add -p low water plants
./todo list            # most urgent first; overdue items are marked
./todo done 1 3        # several at once, four at a time
./todo list -all
./todo stats           # three counts, queried in parallel
```

The table is created on first use. To get a database:
- Homebrew: `brew services start postgresql@18 && createdb todo`
- Docker: `docker run -d -p 5432:5432 -e POSTGRES_HOST_AUTH_METHOD=trust -e POSTGRES_DB=todo postgres:18`, then use `postgres://postgres@localhost:5432/todo?sslmode=disable`

Exit codes: 1 for a missing todo, 2 for bad input, 3 when the database fails.

## Where each feature is

| Feature | Where |
|---|---|
| `effect` functions: implicit `ctx`, one span per call | every method of `Store`, `Service` and `CLI` |
| `effect` interface methods | `Store` in [store.ego](store.ego) |
| An error set with messages, and `check … as` | `TodoError` in [todo.ego](todo.ego), used by [service.ego](service.ego) and [args.ego](args.ego) |
| `check … as Case{…}`: the error becomes a case with a message of its own | `ParseArgs` (a bad ID), `ParseDue` |
| `fail` with an error-set case | `Service.Add`, `ParsePriority`, `ParseArgs` |
| Exhaustive `match`: on errors, on enums, on strings, as an expression | `Report`, `CLI.run` (every `Action`), `ParsePriority`, `PgStore.Count` |
| Enums without data | `Action`, `Priority`, `Filter` |
| `all`: three queries in parallel | `Service.Stats` |
| `retry` with a schedule that skips errors that won't go away, and `Tap` to say it's waiting | `Connect` and `connectRetry` in [store.ego](store.ego) |
| `each`: one call per ID, four at a time | `CLI.run` for `done` and `rm` |
| `check … else { arms }`: a sentinel error becomes a value | `PgStore.found` (`pgx.ErrNoRows => false`) |
| `timeout` around a whole command | `CLI.Run` |
| Command-line mistakes reported before connecting | `ParseArgs` in [args.ego](args.ego), called by `run` in [main.ego](main.ego) |
| Lambdas, with types inferred, even through a generic function | `Service.List`, `MemStore`, `connectRetry`, `scope.Run(ctx, s => …)` in [main.ego](main.ego) |
| `if` expressions, including `else if` chains | `compareDue`, `ParseArgs`, `CLI.list` |
| `?.` and `??` | `CLI.list` (the due date), `databaseURL` |
| f-strings with format specs | `CLI.list`, `CLI.add`, `ParsePriority` |
| Layers: a pool closed with the scope; a test graph with an in-memory store and a fixed clock | [inject.go](inject.go) (plain Go, as injectors are), used by [main.ego](main.ego) and [cli_test.ego](cli_test.ego) |
| Tests written in effect-go: `must`, `match`, f-strings, `check` in a function literal | [parse_test.ego](parse_test.ego), [cli_test.ego](cli_test.ego) |

Tests: `go test ./todo` runs the commands on the in-memory graph. Set
`TODO_TEST_DATABASE_URL` to a database the tests may empty, and the same
commands also run on PostgreSQL.
