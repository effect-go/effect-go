# Milestones 2–7: the dialect, its tools, and layers

*2026-10-02. Plan steps 2–5 of [assessment.md](assessment.md) §9. Week 1 is in [milestone-1.md](milestone-1.md).*

## Verdict

**Every gate passes, with one caveat: the editor gate was tested with a scripted LSP client, not inside VS Code.** The VS Code extension is built and packaged, but it hasn't been run in an editor.

| Gate | Result |
|---|---|
| **2.** `ego fmt` leaves its own output unchanged, and formats every standard library file exactly as gofmt does | Pass. All 8,033 `.go` files under `GOROOT/src` come out byte for byte the same; formatting is idempotent on every dialect sample. |
| **2.** Editor: go to definition, hover, diagnostics and format-on-save on a file using `check` and `effect` | Pass against gopls v0.23.0, driven by a test client that speaks LSP like VS Code does ([proxy_test.go](../internal/lsp/proxy_test.go)). Rename (planned for weeks 3–4) passes too. |
| **3–4.** The generated code passes review | Pass. The dashboard compiles to the code written by hand with the library, down to the closures and labels; `users.ego` compiles to the same code as `users.go`. |
| **3–4.** The demos are at least 30% shorter | Pass. Dashboard: 72 lines with errgroup + backoff, 34 with the library, 17 in the dialect. Users: 66 lines, then 44 (33% shorter). |
| **3–4.** An agent given only AGENTS.md writes correct effect-go for both demos | Pass. The first agent compiled both demos on its second try. After fixes from its feedback, a fresh agent compiled both on the first try. Both runs passed the hidden tests. See [examples/agenttest](../examples/agenttest/README.md). |
| **5.** A missing provider is a build error with a readable message | Pass: `inject.go:8:2: Missing: no provider for *DB, needed by NewService`. Ambiguous providers, cycles and unused providers passed directly to `Build` are errors too. |
| **5.** Shutdown order is correct under synctest | Pass ([app_test.go](../internal/egotest/app/app_test.go)). |
| **6–7.** Each v1 feature lowers to Go you'd accept in review, and `ego fmt` and the proxy handle it | Pass. See [report.ego](../internal/egotest/report/report.ego) and its generated file. The proxy test hovers inside lambdas and f-strings. |
| **6–7.** AGENTS.md still fits on one page, and the agent test still passes | Pass. The page is 81 lines, and the agent test ran with the v1 features in place (the agent used a lambda). |

## What was built

| Part | Where | Notes |
|---|---|---|
| Parser and printer | [internal/syntax](../internal/syntax) | Go 1.27.1's `go/scanner`, `go/parser`, `go/ast`, `go/printer` and `go/format`, with about 1,400 lines added. The first commit is the unmodified copy, so the diff shows only the dialect. |
| Compiler | [internal/lower](../internal/lower) | `ego generate`. See "How the compiler works". |
| Formatter | `ego fmt` | The forked printer. Arms of a `match` line up like composite literal values. |
| Language server | [internal/lsp](../internal/lsp) | `ego lsp`, with templ's architecture: it serves `.ego` files only and runs its own gopls, while the Go extension keeps `.go` files. |
| VS Code extension | [editors/vscode](../editors/vscode) | A TextMate grammar on top of Go's, and a client for `ego lsp`. It packages with vsce. |
| Layers | [layer](../layer), [internal/layers](../internal/layers) | Wire-style: `panic(layer.Build(...))` in an `egolayers` file. |
| Analyzers | [analysis](../analysis), `ego vet` | §4.2's three analyzers, for plain Go. `ego vet` runs `go vet`, then them. |
| Runtime additions | [scope](../scope) | `Scope.Defer`, and `StopTimeout` with `*StuckError` (the fiber deadline missing from week 1). |
| CI | [.github/workflows/ci.yml](../.github/workflows/ci.yml) | On Go 1.26 and 1.27: vet, `-race` tests, `ego generate -check` and both formatters. |

The compiler is tested in three ways:
- **Golden files.** Each package in [internal/egotest](../internal/egotest) commits its generated Go. Its tests run against that Go under `-race`, and the golden test fails if regenerating changes it.
- **Error cases** in `internal/lower/testdata/errors` mark each expected diagnostic with `// ERROR "regexp"`.
- **The examples module** runs every dashboard test against all three versions.

## How the compiler works

- **Plain Go is copied byte for byte.** Only dialect constructs are re-rendered. Small text edits during the copy handle the rest: inserting `ctx`, rewriting an `effect` header, turning an error-set result into `error`. Comments and layout survive, and the output reads like the input.
- **Types come from drafts.** The package is rendered as a draft, type-checked with go/types, then rendered again with what was learned, until the draft stops changing. In practice that takes two or three rounds.
  - Where a type isn't known yet, the draft calls small generic helpers, such as `_egoAll2(_egoV(a()), _egoV(b()))`, which make go/types infer it.
  - A lambda's parameter types come from the parameter it's passed to.
  - Implicit `ctx` is inserted where the callee's first parameter is a `context.Context` that the call leaves out.
  - Dependencies load once, from export data, through `go/packages`.
- **One source map for everything.** It records every verbatim span, plus anchors for generated text. Draft types, compiler diagnostics, `//line` directives and the language server all use it.
- **Every statement becomes one contiguous block.** Expression forms (`if`/`match` expressions, `?.`, `??`) used inside a larger expression are computed into a temporary just before the statement. In a call, earlier arguments with side effects move with them, so evaluation order holds.

## Changes to the plan

- **Step 0's reading of Dingo was skipped.** The parser came from Go's own packages, and the proxy follows templ's architecture. Neither needed Dingo's code.
- **The analyzers (§4.2) were built,** though no milestone listed them. They found the deliberate errgroup bug in the dashboard tests.
- **Answers to open questions (§10):**
  - Generated signatures return `error`.
  - Implicit `ctx` reaches plain Go functions that take a context.
  - Lambdas use `=>`, and a single parameter needs no parentheses.
  - An `if` expression has one expression per branch.
  - f-strings support format specs.
  - The dependency graph is declared with a wire-style marker in plain Go, not a dialect block.
- **Additions the work showed were needed:**
  - **`check … as Case{Field: …}`** gives the case fields of its own, such as a message, while the cause still goes in its error field. This came from writing the todo example.
  - **Error-set cases take an optional message:** `NotFound{ ID UserID } "no user {ID}"`. Without one, `Error()` is generated from the case name and fields.
  - **String literals are allowed inside f-string interpolations:** `f"until {t.Format("2006-01-02")}"`. The agent test hit this.
  - **The runtime packages are imported automatically** (`scope`, `schedule`, `trace`, `layer`). Also from the agent test: without the import, a lambda passed to `schedule.While` couldn't be typed.
  - **Dialect test packages live outside `testdata`,** because gopls doesn't report diagnostics for packages under `testdata`.
- **After the milestones, from writing the todo example and comparing with Effect v4:**
  - `each(items, limit, x => call(x))` and `scope.Each`: Effect's `forEach` with `concurrency`.
  - `check f() else { Case(_) => value }`: Effect's `catchTag`, written with `else` and `match` arms rather than a new keyword (`catch` would read as exceptions).
  - `Schedule.Tap` and a span event per retry: Effect's `Schedule.tap`.
- **Tests can be written in the dialect.** `x_test.ego` files, both in-package and external (`package x_test`), are type-checked the way `go test` builds them. `ego test` regenerates, then runs `go test`.
- **A statement-level `match` panics on an error that is outside the set,** unless it has a `_` arm. An `else if err != nil { panic(err) }` makes the "closed set" promise checkable at run time.

## Limitations and next steps

- **The editor gate needs a real VS Code run.** Install the `.vsix`, open `examples/users/ego/users.ego`, and check hover, definition, diagnostics and format-on-save.
- **`//line` directives break coverage** (golang/go#41222). `-lines=false` turns them off.
- **Not supported yet:**
  - `?.` after a call;
  - dialect expressions in `for` and `case` headers. The compiler says so and suggests a variable.
- **The proxy regenerates on every keystroke** with a cached importer. That is fine for packages the size of the demos, but it isn't measured on large ones.
- **Still open:** an anonymous form of `effect` for goroutines and callbacks (§10), and the first audience.
