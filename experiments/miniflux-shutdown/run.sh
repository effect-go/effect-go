#!/bin/bash
# run.sh BINARY: time how long miniflux takes to stop while a feed refresh
# is stuck on a slow server.
set -u
BIN=$1
S=$(cd "$(dirname "$0")" && pwd)
U="postgres://postgres@127.0.0.1:54329/miniflux?sslmode=disable"
dropdb -h 127.0.0.1 -p 54329 -U postgres --if-exists miniflux && createdb -h 127.0.0.1 -p 54329 -U postgres miniflux
FSBIN=$(mktemp -d)/feedserver
(cd "$S/feedserver" && go build -o "$FSBIN" .) || exit 1
"$FSBIN" > "$S/feedserver.log" 2>&1 & FS=$!
DATABASE_URL=$U RUN_MIGRATIONS=1 CREATE_ADMIN=1 ADMIN_USERNAME=admin ADMIN_PASSWORD=test-password-123 \
  LISTEN_ADDR=127.0.0.1:18081 FETCHER_ALLOW_PRIVATE_NETWORKS=1 HTTP_CLIENT_TIMEOUT=20 POLLING_FREQUENCY=60 \
  "$BIN" > "$S/miniflux.log" 2>&1 & MF=$!
for i in $(seq 50); do curl -sf 127.0.0.1:18081/healthcheck >/dev/null && break; sleep 0.2; done
A="-u admin:test-password-123"
curl -sf $A -X POST 127.0.0.1:18081/v1/feeds -d '{"feed_url":"http://127.0.0.1:18090/feed.xml","category_id":1}' >/dev/null || echo "feed creation failed"
psql -q -h 127.0.0.1 -p 54329 -U postgres miniflux -c "UPDATE feeds SET next_check_at = now() - interval '1 hour'"
curl -sf $A -X PUT 127.0.0.1:18081/v1/feeds/refresh || echo "refresh failed"
sleep 2
grep -q "slow request started" "$S/feedserver.log" || echo "the refresh didn't reach the feed server"
START=$(python3 -c 'import time; print(time.time())')
kill -TERM $MF
wait $MF
echo "exit status $?, stopped in $(python3 -c "import time; print(round(time.time()-$START, 1))")s"
kill $FS 2>/dev/null; wait $FS 2>/dev/null
grep -h "client gave up" "$S/feedserver.log"
