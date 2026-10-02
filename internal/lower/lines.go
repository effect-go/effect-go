package lower

import (
	"bytes"
	"path/filepath"
	"strconv"
)

// addLineDirectives inserts //line directives into generated code wherever
// a statement's line no longer matches its .ego line, so panics, the
// debugger and compiler errors point at the .ego file.
func addLineDirectives(raw []byte, out *Output) []byte {
	name := filepath.Base(out.Ego)
	lineStarts := []int{0}
	for i, c := range raw {
		if c == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	lineOf := func(off int) int { // 0-based
		lo, hi := 0, len(lineStarts)-1
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if lineStarts[mid] <= off {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		return lo
	}
	insert := map[int]int{} // output line -> source line
	delta, have := 0, false
	for _, m := range out.Map.marks {
		l := lineOf(m.out)
		// Only where the statement starts its line.
		if len(bytes.TrimSpace(raw[lineStarts[l]:m.out])) != 0 {
			continue
		}
		if have && l+1+delta == m.line {
			continue
		}
		if _, ok := insert[l]; ok {
			continue
		}
		insert[l] = m.line
		delta, have = m.line-(l+1), true
	}
	var b bytes.Buffer
	for i := range lineStarts {
		if src, ok := insert[i]; ok {
			b.WriteString("//line " + name + ":" + strconv.Itoa(src) + "\n")
		}
		end := len(raw)
		if i+1 < len(lineStarts) {
			end = lineStarts[i+1]
		}
		b.Write(raw[lineStarts[i]:end])
	}
	return b.Bytes()
}
