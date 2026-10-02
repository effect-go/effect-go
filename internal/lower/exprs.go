package lower

import (
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"github.com/effect-go/effect-go/internal/syntax/ast"
)

// expr renders a dialect expression. It reports false for other nodes, which
// the caller renders child by child.
func (g *fileGen) expr(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.CheckExpr:
		kw := "check"
		if x.Must {
			kw = "must"
		}
		g.errorf(x.Pos(), "%s must start a statement or the right side of an assignment", kw)
		g.node(x.X)
	case *ast.ElseExpr:
		g.errorf(x.Else, "else must follow the right side of an assignment: x := f() else fallback")
		g.node(x.X)
	case *ast.IfExpr, *ast.MatchExpr, *ast.CoalesceExpr, *ast.OptSelectorExpr:
		g.w.anchor(g.off(x.Pos()))
		if g.r.final {
			g.hoist(x)
		} else {
			g.draft(x)
		}
	case *ast.LambdaExpr:
		g.w.anchor(g.off(x.Pos()))
		g.lambda(x)
	case *ast.FString:
		g.w.anchor(g.off(x.Pos()))
		g.fstring(x, "", nil)
	case *ast.CallExpr:
		if g.builtin(x) != "" {
			g.w.anchor(g.off(x.Pos()))
			g.combinator(x)
			return true
		}
		g.call(x)
	case *ast.Ident:
		if c := g.matchBinding(x); c != nil && !g.r.final {
			g.w.rec(x, func() { g.w.str("_egoAs[" + g.text(c.Fun) + "](" + g.matchTag(c) + ")") })
			return true
		}
		return false
	default:
		return false
	}
	return true
}

// matchTag returns the source of the tag of the match a pattern belongs to.
func (g *fileGen) matchTag(pat *ast.CallExpr) string {
	switch m := g.parent(g.parent(pat)).(type) {
	case *ast.MatchExpr:
		return g.renderStr(m.Tag)
	case *ast.MatchStmt:
		return g.renderStr(m.Tag)
	}
	return ""
}

// call renders a call. If an argument must be hoisted, the arguments before
// it that have side effects are hoisted first, so evaluation order holds.
func (g *fileGen) call(x *ast.CallExpr) {
	last := -1
	if g.r.final && g.pre != nil {
		for i, a := range x.Args {
			if needsHoist(a) {
				last = i
			}
		}
	}
	if last < 0 {
		g.children(x)
		return
	}
	g.copy(g.off(x.Pos()), g.off(x.Fun.Pos()))
	g.node(x.Fun)
	cur := g.off(x.Fun.End())
	for i, a := range x.Args {
		g.copy(cur, g.off(a.Pos()))
		if i < last && hasCall(a) {
			name := g.temp("arg")
			g.toPre(func() {
				g.w.str(name + " := ")
				g.node(a)
				g.w.str("\n")
			})
			g.w.str(name)
		} else {
			g.node(a)
		}
		cur = g.off(a.End())
	}
	g.copy(cur, g.off(x.End()))
}

// needsHoist reports whether x contains an expression that has to be
// computed by statements before its own.
func needsHoist(x ast.Node) bool {
	found := false
	ast.Inspect(x, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.FuncLit, *ast.LambdaExpr:
			return false
		case *ast.IfExpr, *ast.MatchExpr, *ast.CoalesceExpr, *ast.OptSelectorExpr:
			found = true
		}
		return !found
	})
	return found
}

func hasCall(x ast.Node) bool {
	found := false
	ast.Inspect(x, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit, *ast.LambdaExpr:
			return false
		case *ast.CallExpr:
			found = true
		case *ast.UnaryExpr:
			if n.Op == token.ARROW {
				found = true
			}
		}
		return !found
	})
	return found
}

// toPre renders fn's output into the statements before the current one.
func (g *fileGen) toPre(fn func()) {
	if g.pre == nil {
		g.pre = &writer{} // dropped: no statement to put it before
	}
	savedW, savedPre := g.w, g.pre
	pre, body := &writer{}, &writer{}
	g.w, g.pre = body, pre
	fn()
	g.w, g.pre = savedW, savedPre
	savedPre.add(pre)
	savedPre.add(body)
}

// genStmt renders one generated statement, with the statements its
// expressions hoist placed before it.
func (g *fileGen) genStmt(fn func()) {
	savedW, savedPre := g.w, g.pre
	pre, body := &writer{}, &writer{}
	g.w, g.pre = body, pre
	fn()
	g.w, g.pre = savedW, savedPre
	g.w.add(pre)
	g.w.add(body)
	g.w.str("\n")
}

// hoist computes x into a new variable before the statement, and renders
// the variable.
func (g *fileGen) hoist(x ast.Expr) {
	if g.pre == nil {
		g.errorf(x.Pos(), "this expression can't be used here: assign it to a variable first")
		g.draft(x)
		return
	}
	name := g.temp("v")
	g.toPre(func() { g.lowerInto(name, true, g.typeOf(x), x) })
	g.w.str(name)
}

// lowerInto writes statements that store the value of x in target, which
// they declare if declare is set.
func (g *fileGen) lowerInto(target string, declare bool, t types.Type, x ast.Expr) {
	x = ast.Unparen(x)
	declareVar := func() bool {
		if !declare {
			return true
		}
		if t == nil {
			g.errorf(x.Pos(), "can't tell the type of this expression")
			return false
		}
		g.w.str("var " + target + " " + g.typeString(t) + "\n")
		return true
	}
	switch x := x.(type) {
	case *ast.IfExpr:
		if !declareVar() {
			return
		}
		g.ifInto(target, t, x)
	case *ast.MatchExpr:
		if !declareVar() {
			return
		}
		g.matchInto(target, t, x)
	case *ast.CoalesceExpr:
		g.coalesceInto(target, declare, t, x)
	case *ast.OptSelectorExpr:
		if !declareVar() {
			return
		}
		guards := g.guards(x)
		g.w.str("if " + strings.Join(guards, " && ") + " {\n")
		g.genStmt(func() { g.w.str(target + " = " + g.plain(x)) })
		g.w.str("}\n")
	default:
		if hasOpt(x) && !isOptChain(x) {
			g.errorf(x.Pos(), "?. must end its expression here: assign it to a variable first")
		}
		if isOptChain(x) {
			if !declareVar() {
				return
			}
			g.w.str("if " + strings.Join(g.guards(x), " && ") + " {\n")
			g.genStmt(func() { g.w.str(target + " = " + g.plain(x)) })
			g.w.str("}\n")
			return
		}
		op := " = "
		if declare {
			op = " := "
		}
		g.genStmt(func() {
			g.w.str(target + op)
			g.node(x)
		})
	}
}

// branchInto stores a branch's value in target.
func (g *fileGen) branchInto(target string, t types.Type, x ast.Expr) {
	g.lowerInto(target, false, t, x)
}

func (g *fileGen) ifInto(target string, t types.Type, x *ast.IfExpr) {
	g.w.str("if ")
	g.node(x.Cond)
	g.w.str(" {\n")
	g.branchInto(target, t, x.Then)
	g.w.str("} else ")
	if e, ok := x.Else.(*ast.IfExpr); ok && !x.ElseL.IsValid() {
		g.ifInto(target, t, e)
		return
	}
	g.w.str("{\n")
	g.branchInto(target, t, x.Else)
	g.w.str("}\n")
}

// draft renders an expression form for type-checking, with the draft
// helpers.
func (g *fileGen) draft(x ast.Expr) {
	g.w.rec(x, func() {
		switch x := x.(type) {
		case *ast.IfExpr:
			g.w.str("_egoIf(")
			g.node(x.Cond)
			g.w.str(", ")
			g.node(x.Then)
			g.w.str(", ")
			g.node(x.Else)
			g.w.str(")")
		case *ast.MatchExpr:
			g.w.str("_egoV(_egoOne(")
			for i, a := range x.Arms {
				if i > 0 {
					g.w.str(", ")
				}
				if b, ok := a.Body.(ast.Expr); ok {
					g.node(b)
				} else {
					g.w.str("nil")
				}
			}
			g.w.str("), ")
			g.node(x.Tag)
			for _, a := range x.Arms {
				for _, p := range a.Patterns {
					if c, ok := p.(*ast.CallExpr); ok {
						g.w.str(", _egoAs[" + g.text(c.Fun) + "](nil)")
					} else if !isBlank(p) {
						g.w.str(", ")
						g.node(p)
					}
				}
			}
			g.w.str(")")
		case *ast.CoalesceExpr:
			g.w.str("_egoCo(_egoV(")
			g.node(x.X)
			g.w.str("), ")
			g.node(x.Y)
			g.w.str(")")
		case *ast.OptSelectorExpr:
			g.node(x.X)
			g.w.str(".")
			g.node(x.Sel)
		}
	})
}

func isBlank(x ast.Expr) bool {
	id, ok := x.(*ast.Ident)
	return ok && id.Name == "_"
}

// isOptChain reports whether x is a selector, call or index chain with ?.
// in it.
func isOptChain(x ast.Expr) bool {
	for {
		switch e := x.(type) {
		case *ast.OptSelectorExpr:
			return true
		case *ast.SelectorExpr:
			x = e.X
		case *ast.CallExpr:
			x = e.Fun
		case *ast.IndexExpr:
			x = e.X
		case *ast.ParenExpr:
			x = e.X
		default:
			return false
		}
	}
}

func hasOpt(x ast.Node) bool {
	found := false
	ast.Inspect(x, func(n ast.Node) bool {
		if _, ok := n.(*ast.OptSelectorExpr); ok {
			found = true
		}
		return !found
	})
	return found
}

// guards returns the nil checks a ?. chain needs, outermost first.
func (g *fileGen) guards(x ast.Expr) []string {
	var out []string
	for {
		switch e := x.(type) {
		case *ast.OptSelectorExpr:
			if hasCall(e.X) {
				g.errorf(e.QDot, "?. after a call isn't supported: store the call's result in a variable first")
			}
			out = append([]string{g.plain(e.X) + " != nil"}, out...)
			x = e.X
		case *ast.SelectorExpr:
			x = e.X
		case *ast.CallExpr:
			x = e.Fun
		case *ast.IndexExpr:
			x = e.X
		case *ast.ParenExpr:
			x = e.X
		default:
			return out
		}
	}
}

// plain renders a ?. chain with every ?. as a plain selector.
func (g *fileGen) plain(x ast.Expr) string {
	saved := g.w
	g.w = &writer{}
	g.plainInto(x)
	s := string(g.w.buf)
	g.w = saved
	return s
}

func (g *fileGen) plainInto(x ast.Expr) {
	if o, ok := x.(*ast.OptSelectorExpr); ok {
		g.plainInto(o.X)
		g.w.str(".")
		g.node(o.Sel)
		return
	}
	if !hasOpt(x) {
		g.node(x)
		return
	}
	cur := g.off(x.Pos())
	for _, c := range childNodes(x) {
		g.copy(cur, g.off(c.Pos()))
		if e, ok := c.(ast.Expr); ok {
			g.plainInto(e)
		} else {
			g.node(c)
		}
		cur = g.off(c.End())
	}
	g.copy(cur, g.off(x.End()))
}

// coalesceInto lowers x ?? y.
func (g *fileGen) coalesceInto(target string, declare bool, t types.Type, x *ast.CoalesceExpr) {
	left := ast.Unparen(x.X)
	op := " = "
	if declare {
		op = " := "
	}
	fallback := func() { g.branchInto(target, t, x.Y) }
	commaOK := false
	switch l := left.(type) {
	case *ast.IndexExpr:
		if lt := g.typeOf(l.X); lt != nil {
			_, commaOK = lt.Underlying().(*types.Map)
		}
	case *ast.TypeAssertExpr:
		commaOK = true
	case *ast.CallExpr:
		rs := results(g.typeOf(l))
		if len(rs) == 2 {
			if b, ok := rs[1].Underlying().(*types.Basic); ok && b.Kind() == types.Bool {
				commaOK = true
			}
		}
	}
	switch {
	case isOptChain(left) && declare && isCheap(x.Y) && !nillable(g.typeOf(left)):
		// city := "unknown"; if u != nil { city = u.City }
		g.genStmt(func() {
			g.w.str(target + " := ")
			g.node(x.Y)
		})
		g.w.str("if " + strings.Join(g.guards(left), " && ") + " {\n")
		g.w.str(target + " = " + g.plain(left) + "\n}\n")
	case isOptChain(left):
		if declare {
			if t == nil {
				g.errorf(x.Pos(), "can't tell the type of this expression")
				return
			}
			g.w.str("var " + target + " " + g.typeString(t) + "\n")
		}
		guards := g.guards(left)
		value := g.plain(left)
		if lt := g.typeOf(left); nillable(lt) {
			guards = append(guards, value+" != nil")
		}
		g.w.str("if " + strings.Join(guards, " && ") + " {\n")
		g.w.str(target + " = " + value + "\n")
		g.w.str("} else {\n")
		fallback()
		g.w.str("}\n")
	case commaOK && declare:
		ok := g.temp("ok")
		g.genStmt(func() {
			g.w.str(target + ", " + ok + " := ")
			g.node(left)
		})
		g.w.str("if !" + ok + " {\n")
		fallback()
		g.w.str("}\n")
	case commaOK:
		v, ok := g.temp("v"), g.temp("ok")
		g.w.str("if " + v + ", " + ok + " := ")
		g.node(left)
		g.w.str("; " + ok + " {\n" + target + " = " + v + "\n} else {\n")
		fallback()
		g.w.str("}\n")
	case nillable(g.typeOf(left)):
		g.genStmt(func() {
			g.w.str(target + op)
			g.node(left)
		})
		g.w.str("if " + target + " == nil {\n")
		fallback()
		g.w.str("}\n")
	default:
		g.errorf(x.OpPos, "?? needs a value that can be missing: a pointer, map lookup, type assertion, ?. chain or a call returning (value, ok)")
	}
}

func nillable(t types.Type) bool {
	if t == nil {
		return false
	}
	switch t.Underlying().(type) {
	case *types.Pointer, *types.Interface, *types.Map, *types.Slice, *types.Signature, *types.Chan:
		return true
	}
	return false
}

// fstring renders an interpolated string as fmt.Sprintf, or fmt.Errorf when
// wrap is set ("%w" appended). Without interpolation it's a plain string.
func (g *fileGen) fstring(x *ast.FString, wrap string, extra []string) {
	raw := x.Raw()
	var format strings.Builder
	var args []ast.Expr
	var specs []string
	for _, p := range x.Parts {
		if p.X == nil {
			format.WriteString(strings.ReplaceAll(p.Text, "%", "%%"))
			continue
		}
		args = append(args, p.X)
		specs = append(specs, p.Spec)
		if p.Spec != "" {
			format.WriteString("%" + p.Spec)
		} else {
			format.WriteString(verb(g.typeOf(p.X)))
		}
	}
	quote := func(s string) string {
		if raw {
			return "`" + s + "`"
		}
		return `"` + s + `"`
	}
	if wrap != "" {
		if format.Len() > 0 {
			format.WriteString(": ")
		}
		format.WriteString("%w")
	}
	if len(args) == 0 && wrap == "" {
		g.w.str(quote(format.String()))
		return
	}
	fn := g.pkgRef("fmt") + ".Sprintf("
	if wrap != "" {
		fn = g.pkgRef("fmt") + ".Errorf("
	}
	g.w.str(fn + quote(format.String()))
	for _, a := range args {
		g.w.str(", ")
		g.node(a)
	}
	for _, e := range extra {
		g.w.str(", " + e)
	}
	if wrap != "" {
		g.w.str(", " + wrap)
	}
	g.w.str(")")
}

// lambda renders x => body as a function literal, with types from where the
// lambda is used.
func (g *fileGen) lambda(x *ast.LambdaExpr) {
	f := g.pkg.fact(x)
	if g.r.ti != nil && f.sig == nil {
		if t := g.expected(x); t != nil {
			if sig, ok := t.Underlying().(*types.Signature); ok && sig.Params().Len() == countParams(x) {
				f.sig = sig
			}
		}
	}
	if f.bodyType == nil && g.r.ti != nil {
		if b, ok := x.Body.(ast.Expr); ok {
			f.bodyType = g.typeOf(b)
		}
	}
	typed := allTyped(x)
	if f.sig == nil && !typed {
		if g.r.final {
			g.errorf(x.Pos(), "can't tell the parameter types of this function: write them, as in (x int) => …")
		}
		g.w.rec(x, func() { g.w.str("nil") })
		return
	}
	g.w.rec(x, func() {
		g.w.str("func(")
		// Parameters, grouped by type as in hand-written code: (a, b T).
		type param struct {
			name *ast.Ident
			typ  ast.Expr // explicit type, or nil
			t    string   // inferred type
		}
		var ps []param
		i := 0
		for _, fl := range x.Params.List {
			for k, n := range fl.Names {
				p := param{name: n}
				switch {
				case fl.Type != nil && k == len(fl.Names)-1:
					p.typ = fl.Type
				case fl.Type == nil && f.sig != nil:
					p.t = g.typeString(f.sig.Params().At(i).Type())
				}
				ps = append(ps, p)
				i++
			}
		}
		for j, p := range ps {
			if j > 0 {
				g.w.str(", ")
			}
			g.node(p.name)
			last := j == len(ps)-1
			switch {
			case p.typ != nil:
				g.w.str(" ")
				g.node(p.typ)
			case p.t != "" && (last || ps[j+1].t != p.t || ps[j+1].typ != nil):
				g.w.str(" " + p.t)
			}
		}
		g.w.str(")")
		var res []types.Type
		switch {
		case f.sig != nil:
			for v := range f.sig.Results().Variables() {
				res = append(res, v.Type())
			}
		case f.bodyType != nil:
			res = results(f.bodyType)
		}
		var resTexts []string
		for _, t := range res {
			resTexts = append(resTexts, g.typeString(t))
		}
		_, exprBody := x.Body.(ast.Expr)
		switch {
		case len(resTexts) == 1:
			g.w.str(" " + resTexts[0])
		case len(resTexts) > 1:
			g.w.str(" (" + strings.Join(resTexts, ", ") + ")")
		case exprBody && f.sig == nil && !g.r.final:
			g.w.str(" any") // draft: learn the body's type
		}
		g.w.str(" {\n")
		ft := &ast.FuncType{Params: x.Params}
		g.pushFunc(ft, x.Body)
		g.fn.ctx = g.fn.outer != nil && g.fn.outer.ctx
		g.fn.types = res
		for range res {
			g.fn.results = append(g.fn.results, nil)
		}
		if n := len(res); n > 0 && isError(res[n-1]) {
			g.fn.hasErr = true
			g.fn.results, g.fn.types = g.fn.results[:n-1], g.fn.types[:n-1]
		}
		switch b := x.Body.(type) {
		case *ast.BlockStmt:
			saved := g.pre
			g.pre = nil
			for _, s := range b.List {
				g.node(s)
				g.w.str("\n")
			}
			g.pre = saved
		case ast.Expr:
			ret := "return "
			if len(resTexts) == 0 && (f.sig != nil || g.r.final) {
				ret = ""
			}
			if needsHoist(b) && ret != "" && len(res) == 1 {
				name := g.temp("v")
				g.lowerInto(name, true, res[0], b)
				g.w.str("return " + name + "\n")
			} else {
				// One line, as a hand-written function literal would be.
				g.w.buf = g.w.buf[:len(g.w.buf)-1]
				g.w.str(" " + ret)
				g.node(b)
				g.w.str(" ")
			}
		}
		g.fn = g.fn.outer
		g.w.str("}")
	})
}

func countParams(x *ast.LambdaExpr) int {
	n := 0
	for _, f := range x.Params.List {
		n += len(f.Names)
	}
	return n
}

func allTyped(x *ast.LambdaExpr) bool {
	for _, f := range x.Params.List {
		if f.Type == nil {
			return false
		}
	}
	return true
}

// combinator renders all, race, retry or timeout as a Go call returning the
// branch values and an error.
func (g *fileGen) combinator(x *ast.CallExpr) []types.Type {
	kind := g.builtin(x)
	if g.fn == nil || !g.fn.ctx {
		g.errorf(x.Pos(), "%s needs a ctx: use it in an effect function", kind)
	}
	var vals []types.Type
	g.w.rec(x, func() {
		switch kind {
		case "all":
			if len(x.Args) < 2 {
				g.errorf(x.Pos(), "all needs at least two calls")
			}
			n := len(x.Args)
			if !g.r.final {
				name := "_egoAllN("
				if n >= 2 && n <= 4 {
					name = "_egoAll" + strconv.Itoa(n) + "("
				}
				g.w.str(name)
				for i, a := range x.Args {
					if i > 0 {
						g.w.str(", ")
					}
					g.draftBranch(a)
				}
				g.w.str(")")
				for _, a := range x.Args {
					vals = append(vals, g.branchType(a))
				}
				return
			}
			if n > 4 {
				g.w.str(g.pkgRef(scopePath) + ".All(ctx,\n")
			} else {
				g.w.str(g.pkgRef(scopePath) + ".All" + strconv.Itoa(n) + "(ctx,\n")
			}
			for _, a := range x.Args {
				vals = append(vals, g.task(a))
				g.w.str(",\n")
			}
			g.w.str(")")
			if n > 4 {
				vals = []types.Type{types.NewSlice(vals[0])}
			}
		case "race":
			if !g.r.final {
				g.w.str("_egoRace(")
				for i, a := range x.Args {
					if i > 0 {
						g.w.str(", ")
					}
					g.draftBranch(a)
				}
				g.w.str(")")
				if len(x.Args) > 0 {
					vals = []types.Type{g.branchType(x.Args[0])}
				}
				return
			}
			g.w.str(g.pkgRef(scopePath) + ".Race(ctx,\n")
			for _, a := range x.Args {
				t := g.task(a)
				if vals == nil {
					vals = []types.Type{t}
				}
				g.w.str(",\n")
			}
			g.w.str(")")
		case "retry", "timeout":
			if len(x.Args) != 2 {
				g.errorf(x.Pos(), "%s takes two arguments: %s(%s, call)", kind, kind, map[string]string{"retry": "policy", "timeout": "duration"}[kind])
				g.w.str("nil")
				return
			}
			if !g.r.final {
				if kind == "timeout" {
					g.w.str("_egoTimeout(")
				} else {
					g.w.str("_egoRetry(")
				}
				g.node(x.Args[0])
				g.w.str(", ")
				g.draftBranch(x.Args[1])
				g.w.str(")")
				vals = []types.Type{g.branchType(x.Args[1])}
				return
			}
			if kind == "retry" {
				g.w.str(g.pkgRef(schedulePath) + ".Retry(ctx, ")
			} else {
				g.w.str(g.pkgRef(scopePath) + ".Timeout(ctx, ")
			}
			g.node(x.Args[0])
			g.w.str(", ")
			vals = []types.Type{g.task(x.Args[1])}
			g.w.str(")")
		}
	})
	return vals
}

// draftBranch renders a branch for a draft: its value, through _egoV.
func (g *fileGen) draftBranch(a ast.Expr) {
	g.w.str("_egoV(")
	g.node(a)
	g.w.str(")")
}

// branchType returns the value type of a branch call: T for (T, error), and
// nil for a call returning only an error.
func (g *fileGen) branchType(a ast.Expr) types.Type {
	if c, ok := ast.Unparen(a).(*ast.CallExpr); ok && g.builtin(c) != "" {
		saved := g.w
		g.w = &writer{}
		vals := g.combinator(c)
		g.w = saved
		if len(vals) == 1 {
			return vals[0]
		}
		return nil
	}
	rs := results(g.typeOf(a))
	if len(rs) == 2 && implementsError(rs[1]) {
		return rs[0]
	}
	if len(rs) == 1 && isError(rs[0]) {
		return nil
	}
	if g.r.final {
		g.errorf(a.Pos(), "a branch must be a call returning (value, error) or error")
	}
	return nil
}

// task renders a branch as a scope.Task: a closure taking its own ctx.
func (g *fileGen) task(a ast.Expr) types.Type {
	a = ast.Unparen(a)
	if _, ok := a.(*ast.CallExpr); !ok {
		g.errorf(a.Pos(), "a branch must be a call: it runs lazily, in its own goroutine")
	}
	t := g.branchType(a)
	ctxType := g.pkgRef("context") + ".Context"
	saved := g.fn
	g.fn = &funcState{outer: saved, ctx: true, used: map[string]bool{}, temps: map[string]bool{}}
	if saved != nil {
		g.fn.used = saved.used
		g.fn.temps = saved.temps
	}
	if t == nil {
		g.w.str("func(ctx " + ctxType + ") (struct{}, error) { return struct{}{}, ")
		g.node(a)
		g.w.str(" }")
		g.fn = saved
		return types.NewStruct(nil, nil)
	}
	g.w.str("func(ctx " + ctxType + ") (" + g.typeString(t) + ", error) { return ")
	g.node(a)
	g.w.str(" }")
	g.fn = saved
	return t
}

// autoLabel returns the label check gives a call: the last two parts of the
// callee's name. all and race have none; retry and timeout use the call
// they wrap.
func (g *fileGen) autoLabel(x ast.Expr) string {
	c, ok := ast.Unparen(x).(*ast.CallExpr)
	if !ok {
		return ""
	}
	switch g.builtin(c) {
	case "all", "race":
		return ""
	case "retry", "timeout":
		if len(c.Args) == 2 {
			return g.autoLabel(c.Args[1])
		}
		return ""
	}
	var parts []string
	f := c.Fun
	for {
		switch e := f.(type) {
		case *ast.IndexExpr:
			f = e.X
			continue
		case *ast.IndexListExpr:
			f = e.X
			continue
		case *ast.SelectorExpr:
			parts = append([]string{e.Sel.Name}, parts...)
			f = e.X
			continue
		case *ast.Ident:
			parts = append([]string{e.Name}, parts...)
		case *ast.CallExpr:
			f = e.Fun // a.Query(…).Scan is labelled Query.Scan
			continue
		}
		break
	}
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, ".")
}

// expected returns the type the context of x expects: the parameter it's
// passed to, the variable it's assigned to, or the result it's returned as.
func (g *fileGen) expected(x ast.Expr) types.Type {
	parent := g.parent(x)
	switch p := parent.(type) {
	case *ast.CallExpr:
		tv, ok := g.tv(p.Fun)
		if !ok {
			return nil
		}
		sig, ok := tv.Type.Underlying().(*types.Signature)
		if !ok {
			return nil
		}
		for i, a := range p.Args {
			if a != x {
				continue
			}
			// An implicit ctx shifts the parameters by one.
			if sig.Params().Len() > 0 && isContext(sig.Params().At(0).Type()) && len(p.Args) < sig.Params().Len() {
				i++
			}
			n := sig.Params().Len()
			switch {
			case sig.Variadic() && i >= n-1:
				return sig.Params().At(n - 1).Type().(*types.Slice).Elem()
			case i < n:
				return sig.Params().At(i).Type()
			}
		}
	case *ast.AssignStmt:
		for i, r := range p.Rhs {
			if r == x && i < len(p.Lhs) && p.Tok == token.ASSIGN {
				return g.typeOf(p.Lhs[i])
			}
		}
	case *ast.ValueSpec:
		if p.Type != nil {
			return g.typeOf(p.Type)
		}
	case *ast.ReturnStmt:
		if g.fn != nil {
			for i, r := range p.Results {
				if r == x && i < len(g.fn.types) {
					return g.fn.types[i]
				}
			}
		}
	case *ast.KeyValueExpr:
		if p.Value == x {
			if lit, ok := g.parent(p).(*ast.CompositeLit); ok {
				if st, ok := g.typeOf(lit).Underlying().(*types.Struct); ok {
					if k, ok := p.Key.(*ast.Ident); ok {
						for f := range st.Fields() {
							if f.Name() == k.Name {
								return f.Type()
							}
						}
					}
				}
			}
		}
	}
	return nil
}

// parent returns the node that contains n.
func (g *fileGen) parent(n ast.Node) ast.Node {
	if g.parents == nil {
		g.parents = map[ast.Node]ast.Node{}
		var stack []ast.Node
		ast.Inspect(g.file, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			if len(stack) > 0 {
				g.parents[n] = stack[len(stack)-1]
			}
			stack = append(stack, n)
			return true
		})
	}
	return g.parents[n]
}

// isCheap reports whether evaluating x early is harmless: a literal, a
// name, or a constant expression of them.
func isCheap(x ast.Expr) bool {
	switch x := ast.Unparen(x).(type) {
	case *ast.BasicLit, *ast.Ident:
		return true
	case *ast.FString:
		for _, p := range x.Parts {
			if p.X != nil {
				return false
			}
		}
		return true
	case *ast.SelectorExpr:
		return isCheap(x.X)
	case *ast.UnaryExpr:
		return isCheap(x.X)
	}
	return false
}
