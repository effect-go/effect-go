package lower

import (
	"go/types"
	"sort"
	"strings"

	"github.com/effect-go/effect-go/internal/syntax/ast"
)

type patKind int

const (
	patNil patKind = iota
	patBlank
	patCase  // Case(x): an error set or enum case
	patValue // a constant or value
)

type pattern struct {
	kind patKind
	expr ast.Expr // the pattern
	typ  ast.Expr // Case in Case(x)
	bind string   // x in Case(x), or "_"
}

func (g *fileGen) patterns(a *ast.MatchArm) []pattern {
	var out []pattern
	for _, p := range a.Patterns {
		switch p := p.(type) {
		case *ast.Ident:
			switch p.Name {
			case "nil":
				out = append(out, pattern{kind: patNil, expr: p})
				continue
			case "_":
				out = append(out, pattern{kind: patBlank, expr: p})
				continue
			}
		case *ast.CallExpr:
			bind := "_"
			if len(p.Args) == 1 {
				if id, ok := p.Args[0].(*ast.Ident); ok {
					bind = id.Name
				} else {
					g.errorf(p.Args[0].Pos(), "expected a name or _ to bind the case to")
				}
			} else if len(p.Args) > 1 {
				g.errorf(p.Pos(), "a case pattern binds one name: Case(x) or Case(_)")
			}
			if len(a.Patterns) > 1 && bind != "_" {
				g.errorf(p.Pos(), "an arm with several patterns can't bind a name: use Case(_)")
			}
			out = append(out, pattern{kind: patCase, expr: p, typ: p.Fun, bind: bind})
			continue
		}
		out = append(out, pattern{kind: patValue, expr: p})
	}
	return out
}

// matchMode is how a match lowers: an errors.AsType chain, a type
// assertion chain, or a switch on values.
type matchMode int

const (
	modeErrors matchMode = iota
	modeSum
	modeValues
)

type matchPlan struct {
	mode    matchMode
	tag     string // source of the tag, or of the variable holding it
	arms    []*ast.MatchArm
	pats    [][]pattern
	blank   bool // has a _ arm
	hasNil  bool
	setName string
}

// plan analyzes a match, reports missing cases, and hoists a tag with side
// effects into a variable.
func (g *fileGen) plan(tag ast.Expr, arms []*ast.MatchArm, isExpr bool) *matchPlan {
	p := &matchPlan{arms: arms}
	hasCase := false
	for _, a := range arms {
		ps := g.patterns(a)
		p.pats = append(p.pats, ps)
		for _, pt := range ps {
			switch pt.kind {
			case patBlank:
				p.blank = true
			case patNil:
				p.hasNil = true
			case patCase:
				hasCase = true
			}
		}
	}
	tt := g.typeOf(tag)
	switch {
	case hasCase && (tt == nil || implementsError(tt)):
		p.mode = modeErrors
	case hasCase:
		p.mode = modeSum
	default:
		p.mode = modeValues
	}

	// Exhaustiveness.
	if !p.blank && g.r.final {
		switch p.mode {
		case modeErrors, modeSum:
			set, cases := sumCases(tt)
			if set == nil {
				for _, ps := range p.pats {
					for _, pt := range ps {
						if pt.kind == patCase && set == nil {
							if ct := g.typeOf(pt.typ); ct != nil {
								if s := setOf(ct); s != nil {
									set, cases = sumCases(s)
								}
							}
						}
					}
				}
			}
			if set != nil {
				p.setName = set.Obj().Name()
				covered := map[string]bool{}
				for _, ps := range p.pats {
					for _, pt := range ps {
						if pt.kind == patCase {
							covered[lastName(pt.typ)] = true
							if ct := g.typeOf(pt.typ); ct != nil && setOf(ct) != set {
								g.errorf(pt.typ.Pos(), "%s is not a case of %s", g.text(pt.typ), set.Obj().Name())
							}
						}
					}
				}
				var missing []string
				for _, c := range cases {
					if !covered[c.Name()] {
						missing = append(missing, c.Name())
					}
				}
				if len(missing) > 0 {
					g.errorf(tag.Pos(), "match on %s doesn't handle %s: add an arm for each, or a _ arm", set.Obj().Name(), strings.Join(missing, ", "))
				}
			}
			if isExpr && p.mode == modeErrors && !p.hasNil {
				g.errorf(tag.Pos(), "a match expression on an error needs a nil arm (or a _ arm)")
			}
		case modeValues:
			consts := enumConsts(tt)
			if len(consts) == 0 {
				g.errorf(tag.Pos(), "match on a %s needs a _ arm: only error sets, enums and constants of a named type can be checked for missing cases", typeName(tt))
				break
			}
			covered := map[*types.Const]bool{}
			for _, ps := range p.pats {
				for _, pt := range ps {
					if c, ok := g.objectOf(pt.expr).(*types.Const); ok {
						covered[c] = true
					}
				}
			}
			var missing []string
			for _, c := range consts {
				if !covered[c] {
					missing = append(missing, c.Name())
				}
			}
			if len(missing) > 0 {
				g.errorf(tag.Pos(), "match on %s doesn't handle %s: add an arm for each, or a _ arm", typeName(tt), strings.Join(missing, ", "))
			}
		}
	}

	// The tag is evaluated once.
	if isSimple(tag) {
		p.tag = g.renderStr(tag)
	} else if g.r.final {
		p.tag = g.temp("v")
		g.genStmt(func() {
			g.w.str(p.tag + " := ")
			g.node(tag)
		})
	} else {
		p.tag = g.renderStr(tag)
	}
	return p
}

func typeName(t types.Type) string {
	if t == nil {
		return "value"
	}
	return types.TypeString(t, func(*types.Package) string { return "" })
}

func lastName(x ast.Expr) string {
	switch x := x.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	}
	return ""
}

// isSimple reports whether evaluating x twice is harmless and cheap.
func isSimple(x ast.Expr) bool {
	switch x := ast.Unparen(x).(type) {
	case *ast.Ident:
		return true
	case *ast.SelectorExpr:
		return isSimple(x.X)
	}
	return false
}

// matchStmt lowers a match statement.
func (g *fileGen) matchStmt(s *ast.MatchStmt) {
	if !g.r.final {
		g.draftMatch(s.Tag, s.Arms, func(a *ast.MatchArm) { g.armBody(a, "", nil) })
		return
	}
	p := g.plan(s.Tag, s.Arms, false)
	g.chain(p, false, func(a *ast.MatchArm) { g.armBody(a, "", nil) })
	g.trimNewline()
}

// matchInto stores the value of a match expression in target.
func (g *fileGen) matchInto(target string, t types.Type, x *ast.MatchExpr) {
	p := g.plan(x.Tag, x.Arms, true)
	g.chain(p, true, func(a *ast.MatchArm) { g.armBody(a, target, t) })
}

// armBody renders an arm's body; for an expression, stores it in target.
func (g *fileGen) armBody(a *ast.MatchArm, target string, t types.Type) {
	switch b := a.Body.(type) {
	case *ast.BlockStmt:
		cur := g.off(b.Lbrace) + 1
		for _, st := range b.List {
			g.copy(cur, g.off(st.Pos()))
			g.mark(st.Pos())
			g.node(st)
			cur = g.off(st.End())
		}
		g.copy(cur, g.off(b.Rbrace))
		g.w.str("\n")
	case ast.Stmt:
		g.mark(b.Pos())
		g.node(b)
		g.w.str("\n")
	case ast.Expr:
		if target != "" {
			g.lowerInto(target, false, t, b)
			return
		}
		g.mark(b.Pos())
		g.genStmt(func() { g.node(b) })
	}
}

// chain renders the arms of a planned match.
func (g *fileGen) chain(p *matchPlan, isExpr bool, body func(*ast.MatchArm)) {
	if p.mode == modeValues {
		g.switchArms(p, isExpr, body)
		return
	}
	errors := ""
	if p.mode == modeErrors {
		errors = g.pkgRef("errors")
	}
	first := true
	var blankArm *ast.MatchArm
	for i, a := range p.arms {
		for _, pt := range p.pats[i] {
			var head string
			switch pt.kind {
			case patBlank:
				blankArm = a
				continue
			case patNil:
				head = p.tag + " == nil"
			case patCase:
				typ := g.renderStr(pt.typ)
				if p.mode == modeErrors {
					head = pt.bind + ", ok := " + errors + ".AsType[" + typ + "](" + p.tag + "); ok"
				} else {
					head = pt.bind + ", ok := " + p.tag + ".(" + typ + "); ok"
				}
			case patValue:
				head = p.tag + " == " + g.renderStr(pt.expr)
			}
			if first {
				g.w.str("if " + head + " {\n")
				first = false
			} else {
				g.w.str("} else if " + head + " {\n")
			}
			body(a)
		}
	}
	switch {
	case blankArm != nil && first:
		g.w.str("{\n")
		body(blankArm)
		g.w.str("}\n")
		return
	case blankArm != nil:
		g.w.str("} else {\n")
		body(blankArm)
	case p.hasNil || isExpr:
		g.w.str("} else {\npanic(" + p.tag + ")\n")
	default:
		g.w.str("} else if " + p.tag + " != nil {\npanic(" + p.tag + ")\n")
	}
	g.w.str("}\n")
}

// switchArms renders a match on values as a switch.
func (g *fileGen) switchArms(p *matchPlan, isExpr bool, body func(*ast.MatchArm)) {
	g.w.str("switch " + p.tag + " {\n")
	var blankArm *ast.MatchArm
	for i, a := range p.arms {
		var cases []string
		for _, pt := range p.pats[i] {
			switch pt.kind {
			case patBlank:
				blankArm = a
			case patNil:
				cases = append(cases, "nil")
			default:
				cases = append(cases, g.renderStr(pt.expr))
			}
		}
		if len(cases) == 0 {
			continue
		}
		if containsBreak(a.Body) {
			g.errorf(a.Pos(), "break in a match arm would leave the match, not a loop: use a labeled break")
		}
		g.w.str("case " + strings.Join(cases, ", ") + ":\n")
		body(a)
	}
	switch {
	case blankArm != nil:
		g.w.str("default:\n")
		body(blankArm)
	case isExpr:
		g.w.str("default:\npanic(" + p.tag + ")\n")
	}
	g.w.str("}\n")
}

func containsBreak(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.FuncLit, *ast.LambdaExpr:
			return false
		case *ast.BranchStmt:
			if n.Label == nil && n.Tok.String() == "break" {
				found = true
			}
		}
		return !found
	})
	return found
}

// draftMatch renders a match statement for a draft: each arm as an if, with
// its binding declared.
func (g *fileGen) draftMatch(tag ast.Expr, arms []*ast.MatchArm, body func(*ast.MatchArm)) {
	t := g.renderStr(tag)
	g.w.str("_egoUse(" + t + ")\n")
	for _, a := range arms {
		for _, pt := range g.patterns(a) {
			switch pt.kind {
			case patCase:
				if pt.bind == "_" {
					g.w.str("if _egoUse(_egoAs[" + g.renderStr(pt.typ) + "](" + t + ")); true {\n")
				} else {
					g.w.str("if " + pt.bind + " := _egoAs[" + g.renderStr(pt.typ) + "](" + t + "); true {\n")
					g.w.str("_egoUse(" + pt.bind + ")\n")
				}
			case patValue:
				g.w.str("if " + t + " == " + g.renderStr(pt.expr) + " {\n")
			default:
				g.w.str("{\n")
			}
			body(a)
			g.w.str("}\n")
		}
	}
	g.trimNewline()
}

var _ = sort.Strings
