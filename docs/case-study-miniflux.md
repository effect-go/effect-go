# Case study: miniflux's feed refreshing in effect-go

*2026-10-02. [miniflux](https://github.com/miniflux/v2) at 703fe82: a
self-hosted RSS reader (Apache-2.0, 9.7k stars, 81k lines of Go).*

We ported the part of miniflux that refreshes feeds in the background to
effect-go:
- **the refresh itself:** `RefreshFeed`, its errors, and the database queries only it uses;
- **the worker pool;**
- **the batch refresh** behind `miniflux -refresh-feeds`;
- **the schedulers;**
- **the daemon** that starts and stops all of it.

That is seven files and about 650 lines of the original. The other 80,000 lines stay plain Go and call the ported code as before. That's how a team would adopt effect-go: one subsystem at a time, where it hurts.

## Results

| | Before | After |
|---|---|---|
| Time to stop the daemon (SIGTERM) while one feed is slow | **18.0 s** | **0.0 s** |
| Time to stop `miniflux -refresh-feeds` (Ctrl-C), same feed | 18.1 s | 0.0 s |
| The slow feed after that stop | marked as failing | untouched; refreshed next time |
| Background loops left running until the process exits | 3 (feed scheduler, cleanup scheduler, systemd watchdog) | 0: they stop first |
| A span per feed refresh | no | yes |
| What a failed refresh records (server error, a page that isn't a feed, an unknown feed) | | identical, message for message |
| Where a refresh failure becomes a UI message, and whether it counts against the feed | 8 places in `RefreshFeed` | one exhaustive `match` each |
| miniflux's tests | 1,925 pass | 1,925 pass (`-race` on the ported packages) |
| Lines of code: concurrency (pool, batch refresh, schedulers, daemon) | 282 | 277 in `.ego` (the same behaviour in plain Go, as generated: 340) |
| Lines of code: the refresh and its errors | 154 | 163 in `.ego`, with the error set's 9 lines (plain Go, as generated: 239) |

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
effect daemon(s *scope.Scope, store *storage.Storage) error {
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
	return nil
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

### Errors: a set, and two decisions in one place each

`RefreshFeed` returns a `*locale.LocalizedErrorWrapper`, which pairs an error with a translation key for the UI. In the original, each failure built one where it happened (8 times), and each one either recorded the error on the feed or didn't. Whether a given failure counted against the feed was spread over the function:

```go
originalFeed, storeErr := store.FeedByID(userID, feedID)
if storeErr != nil {
	return locale.NewLocalizedErrorWrapper(storeErr, "error.database_error", storeErr)
}
…
updatedFeed, parseErr := parser.ParseFeed(responseHandler.EffectiveURL(), bytes.NewReader(responseBody))
if parseErr != nil {
	localizedError := locale.NewLocalizedErrorWrapper(parseErr, "error.unable_to_parse_feed", parseErr)
	if errors.Is(parseErr, parser.ErrFeedFormatNotDetected) {
		localizedError = locale.NewLocalizedErrorWrapper(parseErr, "error.feed_format_not_detected", parseErr)
	}
	return getTranslatedLocalizedError(store, userID, originalFeed, localizedError)
}
```

After, the work is in `refresh`, which returns an error set, so a failure is one line:

```go
error RefreshError {
	NotFound{} "fetcher: feed not found"
	Storage{ Cause error } "{Cause}" // before the fetch
	Fetch{ Err *locale.LocalizedErrorWrapper } "{Err.Error()}"
	Body{ Err *locale.LocalizedErrorWrapper } "{Err.Error()}" // reading the response
	Duplicated{} "fetcher: duplicated feed"
	Parse{ Cause error } "{Cause}"
	Save{ Cause error } "{Cause}" // storing the result
}

updatedFeed := check parser.ParseFeed(responseHandler.EffectiveURL(), bytes.NewReader(responseBody)) as Parse
```

`RefreshFeed` keeps its signature, so the UI and the API don't change, and makes the two decisions with exhaustive matches:

```go
recorded := match err {
	nil                                        => false
	Fetch(_), Duplicated(_), Parse(_), Save(_) => ctx.Err() == nil // a stopped refresh isn't the feed's fault
	NotFound(_), Storage(_), Body(_)           => false
}
```

A new case won't compile until both matches handle it. Writing the first one also brought out an inconsistency nobody had to face before: an error while *reading* the response (`Body`) isn't recorded on the feed, while an error *getting* it (`Fetch`) is. We kept that behaviour; the arm now says so, where a maintainer can see it and decide.

The queries only the refresh uses became `effect` methods: `WeeklyFeedEntryCount`, `UpdateFeedError` and `RefreshFeedEntries`, whose per-entry transactions now start with `BeginTx(ctx, nil)`. A stopped refresh rolls back the entry in progress and stops.

We checked the error paths against the original on a real database: a feed whose server answers 500, a page that isn't a feed, and an unknown feed record the same message and count, and return the same HTTP status ([errors.sh](../experiments/miniflux-shutdown/errors.sh)).

## What effect-go didn't do for us

- **Cancellation still takes judgement.** With the refresh cancellable, a shutdown would have been recorded as the feed's fetch error. The compiler gives you the context; deciding what a cancelled operation means is still yours. We added one check: a refresh stopped by its context isn't recorded against the feed.
- **The UI's error type stays at the boundary.** miniflux's `LocalizedErrorWrapper` isn't an `error` (its `Error()` returns an `error`, not a string), and the UI and API depend on it. The port converts to it in one function, `localize`, instead of redesigning it across the codebase.
- **Most queries still run to the end.** Only the three queries the refresh alone uses take a context. `FeedByID`, `UpdateFeed` and `UserByID` (80 callers) don't. Giving them one changes their signature for every caller: that's a refactoring effect-go makes cheaper inside effect functions (the `ctx` is passed for you) but not free.
- **The line count isn't the point of the error half.** The refresh is 9 lines longer, the size of the error set. What it buys is the decisions in one place, checked for completeness by the compiler.
- **Dependency injection with `layer` didn't pay here.** We moved the daemon's wiring (the worker pool, then the web servers, each stopped by the scope) to an injector. The generated code does what the hand-written code did. It took 45 lines to replace 12:
  - wrappers for constructors that take configuration values (`worker.NewPool(store, n)`) or return two results (`server.StartWebServer`);
  - a struct to return both services, since an injector returns one value.

  miniflux's real dependencies are globals (`config.Opts` is read 249 times in 79 files), its explicit graph is a chain of five constructors, and no test builds a variant of it. `layer` pays off when a graph is wide and tests swap parts of it, as in the [todo example](../examples/todo/inject.go). Here, we'd keep the 12 lines. Making configuration a dependency instead of a global would come first, and that's a refactoring of miniflux, not a port.
- **One fire-and-forget goroutine remains.** `go integration.PushEntries(…)` sends new entries to third-party services. Bringing it into the scope would mean deciding whether shutdown should wait for those pushes, which is a product decision, not a mechanical change.

## Reproducing it

The port is the `effect-go` branch of a local clone of miniflux at 703fe82 (its last commit is the `layer` experiment above). Its `go.mod` points at a local effect-go with a `replace` directive until effect-go is published. Steps:
1. `ego generate ./internal/...` regenerates the Go.
2. `go test ./internal/...` runs miniflux's tests.
3. [experiments/miniflux-shutdown](../experiments/miniflux-shutdown) has the test feed server, the script that times SIGTERM (`run.sh`) and the one that compares recorded errors (`errors.sh`).
