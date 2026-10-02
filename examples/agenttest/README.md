# Agent test

The plan's week 3–4 gate: an agent given only [AGENTS.md](../../AGENTS.md)
writes correct effect-go for both demos.

A fresh agent got AGENTS.md, `dashboard/types.go` and a prose spec of each
demo, and was allowed to run `ego generate` and `go vet`, but not to read
other files or run tests. In the first run, each file compiled
on the second `ego generate` run. The tests
here, hidden from the agent and taken from the hand-written demos, then
passed unchanged: 7 dashboard tests (parallelism, cancellation, retries,
timeout, every failure reported, nothing outlives the call) and 2 users
tests.

The two errors it hit, and its notes on what AGENTS.md left unclear, led to
fixes: string literals inside f-string interpolations now work, runtime
packages such as `schedule` are imported automatically (which also lets a
lambda's types be inferred there), and AGENTS.md now states labels, retry
counts, `race`'s errors and `timeout`'s error.

The files here are from a second run, after those fixes, with a fresh agent
and the same spec: both compiled on the first `ego generate` run and passed
the same hidden tests.
