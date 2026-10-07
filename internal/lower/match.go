package lower

import (
	"go/token"
	"go/types"
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
	mode   matchMode
	tag    string // source of the tag, or of the variable holding it
	arms   []*ast.MatchArm
	pats   [][]pattern
	blank  bool // has a _ arm
	hasNil bool
	rest   string // statement for errors no arm matches; panic if empty
}

// plan analyzes a match, reports missing cases, and hoists a tag with side
// effects into a variable.
func (g *fileGen) plan(tag ast.Expr, arms []*ast.MatchArm, isExpr bool) *matchPlan {
	p := &matchPlan{arms: arms}
	tt := g.typeOf(tag)
	hasCase := g.planArms(p)
	switch {
	// On an error, sentinel arms (io.EOF) use errors.Is too.
	case hasCase && tt == nil, tt != nil && types.IsInterface(tt) && implementsError(tt):
		p.mode = modeErrors
	case hasCase:
		p.mode = modeSum
	default:
		p.mode = modeValues
	}
	if !p.blank && g.r.final {
		g.exhaustive(p, tt, tag, tag.Pos(), isExpr)
	}

	// The tag is evaluated once.
	if isSimple(tag) || p.mode == modeValues {
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

// planArms reads the patterns of p's arms, and reports whether one is a
// case pattern.
func (g *fileGen) planArms(p *matchPlan) (hasCase bool) {
	for _, a := range p.arms {
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
			case patValue:
			}
		}
	}
	return hasCase
}

// exhaustive reports the cases a match without a _ arm doesn't handle.
// For a match on a plain error, from is where the error comes from: it
// must be known to hold only errors of the set the arms' cases belong to.
func (g *fileGen) exhaustive(p *matchPlan, tt types.Type, from ast.Expr, at token.Pos, isExpr bool) {
	switch p.mode {
	case modeErrors, modeSum:
		set, cases := sumCases(tt)
		if set == nil {
			for _, ps := range p.pats {
				for _, pt := range ps {
					if pt.kind == patCase && set == nil {
						if ct := g.caseType(pt.typ); ct != nil {
							if s := setOf(ct); s != nil {
								set, cases = sumCases(s)
							}
						}
					}
				}
			}
			if set != nil && p.mode == modeErrors && from != nil {
				if src, why := g.errSource(from); !sameSet(src, set) {
					fix := "add a _ arm"
					if why == whyParam {
						fix = "declare it as " + set.Obj().Name() + ", or add a _ arm"
					}
					if why != "" {
						why = " (" + why + ")"
					}
					g.errorf(at, "%s can hold errors that aren't cases of %s%s: %s", g.text(from), set.Obj().Name(), why, fix)
					return
				}
			}
		}
		if set != nil {
			covered := map[string]bool{}
			for _, ps := range p.pats {
				for _, pt := range ps {
					if pt.kind == patCase {
						covered[lastName(pt.typ)] = true
						if ct := g.caseType(pt.typ); ct != nil && setOf(ct) != set {
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
				g.errorf(at, "match on %s doesn't handle %s: add an arm for each, or a _ arm", set.Obj().Name(), strings.Join(missing, ", "))
			}
		}
		if isExpr && p.mode == modeErrors && !p.hasNil {
			g.errorf(at, "a match expression on an error needs a nil arm (or a _ arm)")
		}
	case modeValues:
		consts := enumConsts(tt)
		if len(consts) == 0 {
			g.errorf(at, "match on a %s needs a _ arm: only error sets, enums and constants of a named type can be checked for missing cases", typeName(tt))
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
			g.errorf(at, "match on %s doesn't handle %s: add an arm for each, or a _ arm", typeName(tt), strings.Join(missing, ", "))
		}
	}
}

// caseType returns the type a case pattern names. The patterns of match
// expressions aren't in the drafts, so it also looks the name up.
func (g *fileGen) caseType(x ast.Expr) types.Type {
	if t := g.typeOf(x); t != nil {
		return t
	}
	if g.r.ti == nil {
		return nil
	}
	scope := g.r.ti.pkg.Scope()
	if sel, ok := x.(*ast.SelectorExpr); ok {
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return nil
		}
		scope = nil
		for _, imp := range g.r.ti.pkg.Imports() {
			if g.names[imp.Path()] == id.Name {
				scope = imp.Scope()
			}
		}
		if scope == nil {
			return nil
		}
		x = sel.Sel
	}
	if id, ok := x.(*ast.Ident); ok {
		if tn, ok := scope.Lookup(id.Name).(*types.TypeName); ok {
			return tn.Type()
		}
	}
	return nil
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

// laterUse reports whether an arm after the i-th uses name without binding
// it itself: it then means another variable, which the i-th arm's if must
// not hide.
func laterUse(p *matchPlan, i int, name string) bool {
	for k := i + 1; k < len(p.arms); k++ {
		bound := false
		for _, pt := range p.pats[k] {
			bound = bound || pt.kind == patCase && pt.bind == name
		}
		if !bound && usesName(p.arms[k].Body, name) {
			return true
		}
	}
	return false
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
	var bind string // the arm's binding, declared in its block
	for i, a := range p.arms {
		for _, pt := range p.pats[i] {
			switch pt.kind {
			case patBlank:
				blankArm = a
				continue
			}
			if first {
				g.w.str("if ")
				first = false
			} else {
				g.w.str("} else if ")
			}
			bind = ""
			// Patterns are copied, so the editor can navigate from them.
			switch pt.kind {
			case patNil:
				g.w.str(p.tag + " == nil")
			case patCase:
				// An if's init variables stay in scope in its else
				// branches, the later arms: fresh names there, and the
				// arm's own name inside its block.
				ok, v := "ok", pt.bind
				if laterUse(p, i, "ok") {
					ok = g.temp("ok")
				}
				if pt.bind != "_" && laterUse(p, i, pt.bind) {
					v = g.temp(pt.bind)
				}
				if p.mode == modeErrors {
					g.w.str(v + ", " + ok + " := " + errors + ".AsType[")
					g.node(pt.typ)
					g.w.str("](" + p.tag + "); " + ok)
				} else {
					g.w.str(v + ", " + ok + " := " + p.tag + ".(")
					g.node(pt.typ)
					g.w.str("); " + ok)
				}
				if v != pt.bind {
					bind = pt.bind + " := " + v + "\n"
				}
			case patValue:
				if p.mode == modeErrors {
					// A sentinel error such as io.EOF, wrapped or not.
					g.w.str(errors + ".Is(" + p.tag + ", ")
					g.node(pt.expr)
					g.w.str(")")
				} else {
					g.w.str(p.tag + " == ")
					g.node(pt.expr)
				}
			case patBlank: // handled above
			}
			g.w.str(" {\n" + bind)
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
	case p.rest != "":
		g.w.str("} else {\n" + p.rest + "\n")
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
		var cases []pattern
		for _, pt := range p.pats[i] {
			if pt.kind == patBlank {
				blankArm = a
			} else {
				cases = append(cases, pt)
			}
		}
		if len(cases) == 0 {
			continue
		}
		if containsBreak(a.Body) {
			g.errorf(a.Pos(), "break in a match arm would leave the match, not a loop: use a labeled break")
		}
		g.w.str("case ")
		for j, pt := range cases {
			if j > 0 {
				g.w.str(", ")
			}
			g.node(pt.expr)
		}
		g.w.str(":\n")
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
	g.w.str("_egoUse(")
	g.node(tag)
	g.w.str(")\n")
	g.draftArms(t, arms, body)
}

// draftArms renders arms matching the value t for a draft.
func (g *fileGen) draftArms(t string, arms []*ast.MatchArm, body func(*ast.MatchArm)) {
	for _, a := range arms {
		for _, pt := range g.patterns(a) {
			switch pt.kind {
			case patCase:
				if pt.bind == "_" {
					g.w.str("if _egoUse(_egoAs[")
				} else {
					g.w.str("if " + pt.bind + " := _egoAs[")
				}
				g.node(pt.typ)
				g.w.str("](" + t + ")")
				if pt.bind == "_" {
					g.w.str("); true {\n")
				} else {
					g.w.str("; true {\n_egoUse(" + pt.bind + ")\n")
				}
			case patValue:
				g.w.str("if " + t + " == ")
				g.node(pt.expr)
				g.w.str(" {\n")
			default:
				g.w.str("{\n")
			}
			body(a)
			g.w.str("}\n")
		}
	}
	g.trimNewline()
}
