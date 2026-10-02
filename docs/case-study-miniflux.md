# Case study: miniflux's feed refreshing in effect-go

*2026-10-02. [miniflux](https://github.com/miniflux/v2) at 703fe82: a
self-hosted RSS reader (Apache-2.0, 9.7k stars, 81k lines of Go).*

We ported the part of miniflux that refreshes feeds in the background to
effect-go:
- **the refresh itself:** `RefreshFeed`;
- **the worker pool;**
- **the batch refresh** behind `miniflux -refresh-feeds`;
- **the schedulers;**
- **the daemon** that starts and stops all of it.

That is five files and about 360 lines. The other 80,000 lines stay plain Go and call the ported code as before. That's how a team would adopt effect-go: one subsystem at a time, where it hurts.

## Results

| | Before | After |
|---|---|---|
| Time to stop the daemon (SIGTERM) while one feed is slow | **18.0 s** | **0.0 s** |
| Time to stop `miniflux -refresh-feeds` (Ctrl-C), same feed | 18.1 s | 0.0 s |
| The slow feed after that stop | marked as failing | untouched; refreshed next time |
| Background loops left running until the process exits | 3 (feed scheduler, cleanup scheduler, systemd watchdog) | 0: they stop first |
| A span per feed refresh | no | yes |
| miniflux's tests | 1,925 pass | 1,925 pass (`-race` on the ported packages) |
| Lines of code in the ported files | 282 | 277 in `.ego` (the same behaviour in plain Go, as generated: 340) |

**How we measured.** We ran miniflux on a throwaway PostgreSQL with one feed, served by a local server that answers the first request and then hangs. We started a refresh of all feeds through the API, sent SIGTERM two seconds later, and timed the exit. Miniflux's HTTP client timeout was 20 s, its default. Before the port, nothing could interrupt the refresh, so shutdown waited until the client timeout fired and then recorded the timeout as the feed's error.

**What the lines show.** The ported files do more than the originals (every loop now stops when asked, and every step has a span) in slightly fewer lines. Hand-written Go with the same behaviour is the generated code, which is 23% longer than the `.ego` source.

The first version of the port was 30 lines *longer* than the original, because each of its three ticker loops (feed scheduler, cleanup scheduler, watchdog) was written out with `select` on the context. That's how `repeat` and `scope.Main` came about: the port showed the gaps, and both are now in the library and the dialect.

## What changed

### Shutdown: one scope instead of five goroutines nobody stops

Before:
- **`daemon.go`** started the scheduler, the cleanup loop, the metrics collector and the watchdog with `go`, then waited for a signal in a `for`/`select` loop.
- **On shutdown,** it stopped the HTTP servers and the pool by hand. The `time.Tick` loops and the watchdog's `for { …; time.Sleep }` never stopped.
- **`pool.Shutdown()`** waited for the jobs in progress, which nothing could cancel.

After:

```go
effect daemon(s *scope.Scope, store *storage.Storage) (struct{}, error) {
	pool := worker.NewPool(store, config.Opts.WorkerPoolSize())
	s.Defer(_ => { pool.Shutdown(); return nil })

	var certReloadFn func()
	if config.Opts.HasHTTPService() {
		var httpServers []*http.Server
		httpServers, certReloadFn = server.StartWebServer(store, pool)
		s.Defer(c => shutdownServers(c, httpServers))
	}
	…
	check all(runScheduler(store, pool), gatherMetrics(store), watchdog(store), reloadCertificates(certReloadFn))
	return struct{}{}, nil
}
```

`startDaemon` is `scope.Main(s => daemon(s, store))`: a scope whose context SIGINT and SIGTERM cancel. Every service returns when that context ends. The scope then closes the HTTP servers, then the pool, in reverse order of creation, as the original did by hand.

### The schedulers: `repeat` instead of ticker loops

Before:

```go
func feedScheduler(store *storage.Storage, pool *worker.Pool, frequency time.Duration, batchSize, errorLimit, limitPerHost int) {
	for range time.Tick(frequency) {
		…
	}
}
```

After:

```go
check all(
	repeat(schedule.Spaced(config.Opts.PollingFrequency()).Delayed(), pushBatch(store, pool)),
	repeat(schedule.Spaced(config.Opts.CleanupFrequency()).Delayed(), cleanup(store)),
)
```

`repeat` calls the function after each delay of the schedule; `.Delayed()` waits before the first call, as `time.Tick` does. The loops stop when the context ends, which `time.Tick` never did. The systemd watchdog is a `repeat` too.

### The batch refresh: `each` instead of a hand-made pool

Before, `refreshFeeds` built a pool by hand:
- a buffered channel of jobs;
- a `sync.WaitGroup`;
- one goroutine per worker, ranging over the channel;
- a loop filling the channel, then `close` and `wg.Wait()`.

After:

```go
check each(jobs, config.Opts.WorkerPoolSize(), job => refreshFeed(store, job))
```

`refreshFeed` logs a failed feed and returns nil, so one bad feed doesn't stop the others, as in the original. When the context is cancelled, the refreshes in progress stop and the remaining ones never start.

### The refresh: a context from the top to the HTTP request

Before, `RefreshFeed(store, userID, feedID, force)` had no context, so its HTTP request could only end by itself or by timeout.

After, it is an `effect` function: it takes a `ctx`, which the fetch uses, and records a span. Two other changes went with it:
- **The fetcher** gained one method, `ExecuteRequestContext`, which is `ExecuteRequest` with `http.NewRequestWithContext`.
- **Callers:**
  - the worker and the batch refresh are `effect` functions, so they pass their `ctx` without writing it;
  - the HTTP handlers in plain Go now pass `r.Context()`, so a request the browser abandons stops its refresh too.

The pool's workers became `effect` functions run with `each`. `Shutdown` cancels them, which cancels their refreshes. `PushContext` lets the scheduler stop even while it waits for a free worker. `Push` keeps its behaviour for the HTTP handlers, which call it in a goroutine after responding.

## What effect-go didn't do for us

- **Cancellation still takes judgement.** With the refresh cancellable, a shutdown would have been recorded as the feed's fetch error. The compiler gives you the context; deciding what a cancelled operation means is still yours. We added one check: a refresh stopped by its context isn't recorded against the feed.
- **Error handling stayed as it was.** miniflux's `LocalizedErrorWrapper` carries a translation key for the UI, and its `Error()` returns an `error`, not a string. So it isn't an `error`, and `check`, `else` and error sets don't apply to it without redesigning the error type across the UI and API. A port that went further would start there.
- **The database isn't cancellable yet.** The storage layer doesn't take a context, so its queries still run to the end. That would be the next step: storage methods as `effect` methods, with their callers unchanged.
- **One fire-and-forget goroutine remains.** `go integration.PushEntries(…)` sends new entries to third-party services. Bringing it into the scope would mean deciding whether shutdown should wait for those pushes, which is a product decision, not a mechanical change.

## Reproducing it

The port is the `effect-go` branch of a local clone of miniflux at 703fe82. Its `go.mod` points at a local effect-go with a `replace` directive until effect-go is published. Steps:
1. `ego generate ./internal/...` regenerates the Go.
2. `go test ./internal/...` runs miniflux's tests.
3. [experiments/miniflux-shutdown](../experiments/miniflux-shutdown) has the slow feed server and the script that times SIGTERM.
