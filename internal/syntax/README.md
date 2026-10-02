# internal/syntax

Copies of Go 1.27.1's `go/ast`, `go/scanner`, `go/parser`, `go/printer` and
`go/format` (BSD license, see LICENSE-GO), extended with the effect-go dialect.
`go/token` is not copied: positions and file sets are shared with the standard
library, so go/types can check the lowered code.

The first commit adding this directory is the unmodified copy, so
`git diff <that commit> -- internal/syntax` shows every dialect change.
