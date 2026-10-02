# miniflux shutdown timing

Times how long miniflux takes to stop while a feed refresh is stuck on a
slow server ([the case study](../../docs/case-study-miniflux.md)).

Needs a PostgreSQL on 127.0.0.1:54329 that trusts the user `postgres`, and a
miniflux binary:

```bash
./run.sh /path/to/miniflux
```

`feedserver` answers the first request for its feed at once and later ones
after 60 seconds, so the refresh that run.sh triggers hangs until miniflux
gives up on it.
