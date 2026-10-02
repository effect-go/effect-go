# Milestone 1: runtime library and demos

*2026-10-01. Plan step 1 of [assessment.md](assessment.md) §9.*

## Verdict

**The week-1 gate passes.** On the dashboard demo, the effectgo runtime does everything errgroup + cenkalti/backoff does in half the code. It also fixes five problems the baseline has, each shown by a test.

## What was built

| Package | Contents | Tests |
|---|---|---|
| [`scope`](../scope) | `Run`, `Fork`/`Join`/`Interrupt`, `All`, `All2`–`All4`, `Race`, `Timeout`, `Acquire`; `Panic`, `TimeoutError`, `KindOf` (Fail, Die, Interrupt) | 14 |
| [`schedule`](../schedule) | `Exponential`, `Spaced`, `Recurs`, `Min`, `Max`, `Jittered`, `While`, `UpTo`, `Retry` | 7 |
| [`trace`](../trace) | `Start` and `End` over OpenTelemetry; `defer trace.End(span, &err)` also records panics | 1 |
| [`examples/dashboard`](../examples/dashboard) | The same page loader written with errgroup + backoff and with effectgo | 10 |
| [`examples/users`](../examples/users) | A user service with a hand-written error set, plus `users.ego`, the same code in the dialect | 2 |

All of it:
- targets Go 1.26; `go vet` includes the check for too-new standard library symbols;
- passes with the race detector;
- runs every test under `testing/synctest`. Time is fake, so tests take milliseconds and assert exact durations. A goroutine still running when a test ends fails the test, so leaks can't pass silently.

The library depends only on OpenTelemetry. errgroup and backoff are used only by the examples, which are a separate module.

## Dashboard: errgroup + backoff vs effectgo

The same behaviour:
- load the user, orders and recommendations in parallel;
- retry recommendations with jittered exponential backoff, 3 times, except when the error is `ErrRejected`;
- race two CDNs for the banner;
- give up after 2 seconds.

| | errgroup + backoff | effectgo |
|---|---|---|
| Code (non-blank, non-comment lines) | 72 | 34 |
| Runs in parallel, cancels the rest on failure, retries, times out | yes | yes |
| Two calls fail at once | reports only the first | reports both |
| When the call returns | the losing CDN request may still be running | everything it started has stopped |
| A panic in a parallel call | crashes the whole process (tested in a subprocess) | comes back to the caller with its original stack, where recovery middleware can handle it |
| Passing `ctx` instead of `gctx` | compiles; the page then waits for a call that should have been cancelled (1 s instead of 10 ms in the test) | each task's `ctx` parameter hides the outer one, so the mistake can't be written by name |
| Not retrying some errors | wrap them in `backoff.Permanent` at the call site | `While(…)` on the policy, a reusable value |
| Racing two calls | a hand-written helper, 25 lines, with a buffered channel to avoid a leak | `scope.Race` |

**The trade-off:** because effectgo waits for cancelled work to stop, a call that is slow to notice cancellation delays the return. In the test, a CDN request that takes 100 ms to stop delays the return by 100 ms. The baseline returns immediately and leaves the request running.

## Users: plain Go vs the dialect

`users.go` is 66 lines and `users.ego` is 39, which is 41% shorter. The plan's week-3 gate asks for 30%. `users.ego` doesn't compile yet; `ego generate` (weeks 2–4) will turn it into Go equivalent to `users.go`.

The handler shows the gap the dialect closes: Go doesn't check that every `UserError` case is handled. A new case falls through to the 500 branch, while `match` in `users.ego` would fail the build.

## Differences from the design doc

- **The Cause model is a classification, not one struct.** It's `KindOf(err)` plus `*Panic` and `*TimeoutError`, and parallel failures are combined with `errors.Join`. Plain Go code uses it with `errors.Is` and `errors.As`, with nothing new to learn.
- **`trace.End` takes `&err`,** so `defer trace.End(span, &err)` is one line and also records panics. The doc and the playground now generate this form.
- **Not built yet:** a deadline for fibers that ignore cancellation, with a report of the ones that don't stop (§3 item 5). Today a scope waits for them indefinitely, which is cooperative cancellation as in all Go code.
- **Not released yet:** the module path is the placeholder `effectgo` until the project's name is chosen (§9 step 0).

## Next

Step 0's open item: the name and first audience, which give the module path for the first release. Then week 2: read Dingo's parser and gopls proxy, fork Go's parser, and test the editor proxy on `check` and `effect`.
