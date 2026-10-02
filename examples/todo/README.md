# todo

A todo list on PostgreSQL, written in effect-go: a small but complete app
that uses most of the dialect and the runtime.

```bash
go build -o todo ./todo        # from examples/; the generated *_ego.go files are committed
export TODO_DATABASE_URL=postgres://localhost:5432/todo?sslmode=disable
./todo add buy milk -p high -due 2026-10-10
./todo add -p low water plants
./todo list            # most urgent first; overdue items are marked
./todo done 1
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
| An error set with messages, and `check … as` | `TodoError` in [todo.ego](todo.ego), used by [service.ego](service.ego) |
| `fail` with an error-set case, and with an error value | `Service.Add`, `ParsePriority`, `CLI.dispatch` |
| Exhaustive `match`: on errors, on enums, on strings, as an expression | `CLI.Run`, `PgStore.Count`, `ParsePriority`, `MemStore.Count` |
| Enums without data | `Priority`, `Filter` |
| `all`: three queries in parallel | `Service.Stats` |
| `retry` with a schedule that skips errors that won't go away | `Connect` and `connectRetry` in [store.ego](store.ego) |
| `timeout` around a whole command | `CLI.Run` |
| Lambdas, with types inferred | `Service.List`, `MemStore`, `connectRetry` |
| `if` expressions, including `else if` chains | `compareDue`, `CLI.Run`, `CLI.list` |
| `?.` and `??` | `CLI.list` (the due date), `databaseURL` |
| f-strings with format specs | `CLI.list`, `CLI.add`, `ParsePriority` |
| Layers: a pool closed with the scope; a test graph with an in-memory store and a fixed clock | [inject.go](inject.go), used by [main.go](main.go) and [cli_test.go](cli_test.go) |
| Plain Go calling effect-go code | [main.go](main.go), [cli_test.go](cli_test.go) |
| A test written in effect-go | [parse_test.ego](parse_test.ego) |

Tests: `go test ./todo` runs the commands on the in-memory graph. Set
`TODO_TEST_DATABASE_URL` to a database the tests may empty, and the same
commands also run on PostgreSQL.
