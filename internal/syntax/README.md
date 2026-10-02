# internal/syntax

Copies of Go's `go/ast`, `go/scanner`, `go/parser`, `go/printer` and
`go/format` (BSD license, see LICENSE-GO), extended with the effect-go dialect.
[UPSTREAM](UPSTREAM) names the Go release they come from. `go/token` is not
copied: positions and file sets are shared with the standard library, so
go/types can check the lowered code.

The first commit adding this directory is the unmodified copy of Go 1.27.1,
so `git diff <that commit> -- internal/syntax` shows every dialect change.
Most of the dialect is in files named `ego.go`, with small hooks in the
copied files.

## A new Go release

A release with new syntax breaks `ego fmt` and the compiler on code that uses
it. CI's weekly `newest-go` job formats and compiles the newest standard
library, and fails when that happens. To catch up:

```bash
go run ./internal/syntax/sync go1.28.0   # three-way merge of the release into the copies
go test ./internal/syntax/... ./internal/lower/...
```

The merge takes the release the copies come from, the copies, and the new
release, as `git merge-file` does, and writes the new version to UPSTREAM.
Conflicts are left marked in the files. New syntax may also need the dialect
lowering (`internal/lower`) to handle the new nodes.
