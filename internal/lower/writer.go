package lower

import (
	"bytes"
	"sort"

	"github.com/effect-go/effect-go/internal/syntax/ast"
)

// A segment is output text copied verbatim from the source.
type segment struct{ out, src, n int }

// An anchor marks generated output text that comes from the source construct
// at src.
type anchor struct{ out, src int }

// A rec is the output span of a node rendered in a way the source map can't
// recover, such as a draft helper call.
type rec struct {
	node       ast.Node
	start, end int
}

// A mark says that a statement from source line line starts at out.
type mark struct{ out, line int }

// writer accumulates output and remembers where each piece came from.
type writer struct {
	buf     []byte
	segs    []segment
	anchors []anchor
	recs    []rec
	marks   []mark
}

func (w *writer) str(s string) { w.buf = append(w.buf, s...) }

// copySrc copies src[a:b] verbatim.
func (w *writer) copySrc(src []byte, a, b int) {
	if b <= a {
		return
	}
	if n := len(w.segs); n > 0 {
		last := &w.segs[n-1]
		if last.out+last.n == len(w.buf) && last.src+last.n == a {
			last.n += b - a
			w.buf = append(w.buf, src[a:b]...)
			return
		}
	}
	w.segs = append(w.segs, segment{len(w.buf), a, b - a})
	w.buf = append(w.buf, src[a:b]...)
}

func (w *writer) anchor(src int) { w.anchors = append(w.anchors, anchor{len(w.buf), src}) }

// rec records the output span of node, written by fn.
func (w *writer) rec(node ast.Node, fn func()) {
	start := len(w.buf)
	fn()
	w.recs = append(w.recs, rec{node, start, len(w.buf)})
}

// add appends o's output to w.
func (w *writer) add(o *writer) {
	off := len(w.buf)
	w.buf = append(w.buf, o.buf...)
	for _, s := range o.segs {
		w.segs = append(w.segs, segment{s.out + off, s.src, s.n})
	}
	for _, a := range o.anchors {
		w.anchors = append(w.anchors, anchor{a.out + off, a.src})
	}
	for _, r := range o.recs {
		w.recs = append(w.recs, rec{r.node, r.start + off, r.end + off})
	}
	for _, m := range o.marks {
		w.marks = append(w.marks, mark{m.out + off, m.line})
	}
}

// line returns the 1-based line of the end of the output so far.
func (w *writer) line() int { return bytes.Count(w.buf, []byte{'\n'}) + 1 }

// atLineStart reports whether only spaces and tabs follow the last newline.
func (w *writer) atLineStart() bool {
	i := bytes.LastIndexByte(w.buf, '\n')
	return len(bytes.Trim(w.buf[i+1:], " \t")) == 0
}

// A SourceMap maps offsets between a .ego file and the Go generated from it,
// before formatting.
type SourceMap struct {
	segs    []segment // sorted by out
	anchors []anchor  // sorted by out
	marks   []mark    // sorted by out
}

func newSourceMap(w *writer) *SourceMap {
	m := &SourceMap{segs: append([]segment(nil), w.segs...), anchors: append([]anchor(nil), w.anchors...), marks: append([]mark(nil), w.marks...)}
	sort.SliceStable(m.marks, func(i, j int) bool { return m.marks[i].out < m.marks[j].out })
	sort.Slice(m.segs, func(i, j int) bool { return m.segs[i].out < m.segs[j].out })
	sort.Slice(m.anchors, func(i, j int) bool { return m.anchors[i].out < m.anchors[j].out })
	return m
}

// ToSource maps an output offset to a source offset. exact is false when out
// is in generated text; src is then the construct it came from.
func (m *SourceMap) ToSource(out int) (src int, exact bool) {
	i := sort.Search(len(m.segs), func(i int) bool { return m.segs[i].out+m.segs[i].n > out })
	if i < len(m.segs) && m.segs[i].out <= out {
		s := m.segs[i]
		return s.src + out - s.out, true
	}
	// Generated text: the nearest anchor or segment before it.
	best, bestOut := -1, -1
	j := sort.Search(len(m.anchors), func(j int) bool { return m.anchors[j].out > out })
	if j > 0 {
		best, bestOut = m.anchors[j-1].src, m.anchors[j-1].out
	}
	if i > 0 {
		if s := m.segs[i-1]; s.out+s.n > bestOut {
			best = s.src + s.n
		}
	}
	return max(best, 0), false
}

// ToOutput maps a source offset to an output offset. It prefers the first
// verbatim copy; exact is false when the source text was rewritten.
func (m *SourceMap) ToOutput(src int) (out int, exact bool) {
	for _, s := range m.segs {
		if s.src <= src && src < s.src+s.n {
			return s.out + src - s.src, true
		}
	}
	for _, s := range m.segs { // the end of a segment
		if src == s.src+s.n {
			return s.out + s.n, true
		}
	}
	best, bestSrc := 0, -1
	for _, a := range m.anchors {
		if a.src <= src && a.src > bestSrc {
			best, bestSrc = a.out, a.src
		}
	}
	return best, false
}

// span returns the output span of the source span [a, b), if both ends were
// copied verbatim.
func (m *SourceMap) span(a, b int) (int, int, bool) {
	start, ok1 := -1, false
	for _, s := range m.segs {
		if s.src <= a && a < s.src+s.n {
			start, ok1 = s.out+a-s.src, true
			break
		}
	}
	if !ok1 {
		return 0, 0, false
	}
	for _, s := range m.segs {
		if s.out >= start && s.src < b && b <= s.src+s.n {
			return start, s.out + b - s.src, true
		}
	}
	return 0, 0, false
}
