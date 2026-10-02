#!/bin/bash
# errors.sh BINARY: refresh feeds that fail in different ways through the
# API, and print what miniflux records on each.
set -u
BIN=$1
S=$(cd "$(dirname "$0")" && pwd)
U="postgres://postgres@127.0.0.1:54329/miniflux?sslmode=disable"
dropdb -h 127.0.0.1 -p 54329 -U postgres --if-exists miniflux 2>/dev/null && createdb -h 127.0.0.1 -p 54329 -U postgres miniflux
FSBIN=$(mktemp -d)/feedserver
(cd "$S/feedserver" && go build -o "$FSBIN" .) || exit 1
"$FSBIN" > /dev/null 2>&1 & FS=$!
DATABASE_URL=$U RUN_MIGRATIONS=1 CREATE_ADMIN=1 ADMIN_USERNAME=admin ADMIN_PASSWORD=test-password-123 \
  LISTEN_ADDR=127.0.0.1:18081 FETCHER_ALLOW_PRIVATE_NETWORKS=1 POLLING_FREQUENCY=60 "$BIN" > /dev/null 2>&1 & MF=$!
for i in $(seq 50); do curl -sf 127.0.0.1:18081/healthcheck >/dev/null && curl -sf 127.0.0.1:18090/ok.xml >/dev/null && break; sleep 0.2; done
A="-u admin:test-password-123"
for f in ok 500 garbage; do
  curl -sf $A -X POST 127.0.0.1:18081/v1/feeds -d "{\"feed_url\":\"http://127.0.0.1:18090/$f.xml\",\"category_id\":1}" >/dev/null || echo "creating $f failed"
done
for id in 1 2 3 99; do
  echo "refresh $id: HTTP $(curl -s -o /dev/null -w '%{http_code}' $A -X PUT 127.0.0.1:18081/v1/feeds/$id/refresh)"
done
psql -h 127.0.0.1 -p 54329 -U postgres miniflux -Atc "SELECT id, feed_url, parsing_error_count, parsing_error_msg, (SELECT count(*) FROM entries e WHERE e.feed_id = f.id) FROM feeds f ORDER BY id"
kill $MF $FS 2>/dev/null; wait 2>/dev/null
