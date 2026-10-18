package lower

import (
	"go/token"
	"go/types"
	"maps"
	"strconv"
	"strings"
	"unicode"

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
		g.errorf(x.Pos(), "%s can only start a statement or the right side of an assignment", kw)
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
		g.fstring(x, false, "")
	case *ast.BinaryExpr:
		return g.binary(x)
	case *ast.CallExpr:
		if g.builtin(x) != "" {
			g.w.anchor(g.off(x.Pos()))
			g.combinator(x)
			return true
		}
		return false
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

// binary renders a binary expression whose right operand is computed by
// statements. After && or ||, they run only when Go would evaluate the
// operand; otherwise a left operand with side effects is computed first,
// so evaluation order holds. It reports false for the other expressions,
// rendered child by child.
func (g *fileGen) binary(x *ast.BinaryExpr) bool {
	if !g.r.final || g.pre == nil || !needsHoist(x.Y) {
		return false
	}
	name := g.temp("v")
	switch {
	case x.Op == token.LAND || x.Op == token.LOR:
		cond := name
		if x.Op == token.LOR {
			cond = "!" + name
		}
		g.toPre(func() {
			g.w.str(name + " := ")
			g.node(x.X)
			g.w.str("\nif " + cond + " {\n")
			g.genStmt(func() {
				g.w.str(name + " = ")
				g.node(x.Y)
			})
			g.w.str("}\n")
		})
		g.w.str(name)
	case hasCall(x.X):
		g.toPre(func() {
			g.w.str(name + " := ")
			g.node(x.X)
			g.w.str("\n")
		})
		g.w.str(name + " " + x.Op.String() + " ")
		g.node(x.Y)
	default:
		return false
	}
	return true
}

// needsHoist reports whether x contains an expression that has to be
// computed by statements before its own.
func needsHoist(x ast.Node) bool {
	if x == nil {
		return false
	}
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
	t := g.typeOf(x)
	if g.untyped(x) {
		// if c { 1 } else { 0 } returned as a float64: the variable has
		// the type the constants would get there, not int.
		if e := g.expected(x); e != nil {
			t = e
		}
	}
	name := g.temp("v")
	g.toPre(func() { g.lowerInto(name, true, t, x) })
	g.w.str(name)
}

// untyped reports whether every value x can give is an untyped constant,
// as in if c { 1 } else { 0 }.
func (g *fileGen) untyped(x ast.Expr) bool {
	switch x := ast.Unparen(x).(type) {
	case *ast.IfExpr:
		return g.untyped(x.Then) && g.untyped(x.Else)
	case *ast.MatchExpr:
		for _, a := range x.Arms {
			if b, ok := a.Body.(ast.Expr); !ok || !g.untyped(b) {
				return false
			}
		}
		return true
	case *ast.Ident, *ast.SelectorExpr:
		c, ok := g.objectOf(x).(*types.Const)
		return ok && isUntyped(c.Type())
	}
	return literalType(x) != nil
}

func isUntyped(t types.Type) bool {
	b, ok := t.(*types.Basic)
	return ok && b.Info()&types.IsUntyped != 0
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
		g.optInto(target, declare, t, x)
	default:
		if hasOpt(x) && !isOptChain(x) {
			g.errorf(x.Pos(), "?. must end its expression here: assign it to a variable first")
		}
		if isOptChain(x) {
			if !declareVar() {
				return
			}
			g.optInto(target, declare, t, x)
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

// optInto stores a ?. chain in target: its value, or the zero value when
// the chain stops at a nil, which a declared target already holds.
func (g *fileGen) optInto(target string, declare bool, t types.Type, x ast.Expr) {
	g.w.str("if " + strings.Join(g.guards(x), " && ") + " {\n")
	g.genStmt(func() { g.w.str(target + " = " + g.plain(x)) })
	if !declare && t != nil {
		g.w.str("} else {\n" + target + " = " + g.zero(t) + "\n")
	}
	g.w.str("}\n")
}

// branchInto stores a branch's value in target. One that calls a function
// is on the branch's line, for stack traces: not the closing brace's. A
// branch that keeps target's value stores nothing: x = x is vet's
// self-assignment.
func (g *fileGen) branchInto(target string, t types.Type, x ast.Expr) {
	if g.keeps(target, x) {
		return
	}
	if hasCall(x) {
		g.mark(x.Pos())
	}
	g.lowerInto(target, false, t, x)
}

func (g *fileGen) ifInto(target string, t types.Type, x *ast.IfExpr) {
	elseIf, _ := x.Else.(*ast.IfExpr)
	if x.ElseL.IsValid() {
		elseIf = nil // a value in braces
	}
	if elseIf == nil && g.keeps(target, x.Then) && !g.keeps(target, x.Else) {
		// x = if c { x } else { b }: if !c { x = b }.
		g.w.str("if ")
		g.not(x.Cond)
		g.w.str(" {\n")
		g.branchInto(target, t, x.Else)
		g.w.str("}\n")
		return
	}
	g.w.str("if ")
	g.node(x.Cond)
	g.w.str(" {\n")
	g.branchInto(target, t, x.Then)
	if elseIf != nil {
		g.w.str("} else ")
		g.ifInto(target, t, elseIf)
		return
	}
	g.elseInto(target, t, x.Else)
}

// elseInto ends an if storing a value in target with an else that stores x,
// unless x keeps target's value.
func (g *fileGen) elseInto(target string, t types.Type, x ast.Expr) {
	if g.keeps(target, x) {
		g.w.str("}\n")
		return
	}
	g.w.str("} else {\n")
	g.branchInto(target, t, x)
	g.w.str("}\n")
}

// keeps reports whether storing x in target would keep its value: x is
// the variable or field the assignment being lowered stores in.
func (g *fileGen) keeps(target string, x ast.Expr) bool {
	x = ast.Unparen(x)
	return target != "" && target == g.self && isRef(x) && g.renderStr(x) == target
}

// isRef reports whether x is a variable or a field of one, as a.b.c, which
// reading has no effect.
func isRef(x ast.Expr) bool {
	switch x := ast.Unparen(x).(type) {
	case *ast.Ident:
		return true
	case *ast.SelectorExpr:
		return isRef(x.X)
	}
	return false
}

// not renders the negation of a condition.
func (g *fileGen) not(cond ast.Expr) {
	switch c := ast.Unparen(cond).(type) {
	case *ast.UnaryExpr:
		if c.Op == token.NOT {
			g.node(ast.Unparen(c.X))
			return
		}
	case *ast.Ident, *ast.SelectorExpr, *ast.CallExpr, *ast.IndexExpr:
		g.w.str("!")
		g.node(c)
		return
	}
	g.w.str("!(")
	g.node(cond)
	g.w.str(")")
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
	// A lookup, an assertion or a (v, ok) call misses with !ok, and its
	// value may also be nil: a nil is missing too.
	commaOK := false
	value := g.typeOf(left)
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
				commaOK, value = true, rs[0]
			}
		}
	}
	found := func(v, ok string) string {
		if nillable(value) {
			return ok + " && " + v + " != nil"
		}
		return ok
	}
	switch {
	case isOptChain(left) && commaOK:
		// u?.Tags["k"] ?? "none": the lookup's ok, behind the guards.
		if declare {
			if t == nil {
				g.errorf(x.Pos(), "can't tell the type of this expression")
				return
			}
			g.w.str("var " + target + " " + g.typeString(t) + "\n")
		}
		hit, v, ok := g.temp("found"), g.temp("v"), g.temp("ok")
		g.w.str(hit + " := false\n")
		g.w.str("if " + strings.Join(g.guards(left), " && ") + " {\n")
		g.w.str("if " + v + ", " + ok + " := " + g.plain(left) + "; " + found(v, ok) + " {\n")
		g.w.str(target + " = " + v + "\n" + hit + " = true\n}\n}\n")
		g.w.str("if !" + hit + " {\n")
		fallback()
		g.w.str("}\n")
	case isOptChain(left) && declare && isCheap(x.Y) && !nillable(g.typeOf(left)):
		// city := "unknown"; if u != nil { city = u.City }. A constant
		// fallback, such as 0 for a float64 or "none" for a named string
		// type, is declared with the chain's type: := would give it its
		// default type.
		decl := target + " := "
		if def := literalType(x.Y); def != nil {
			if lt := g.typeOf(left); lt != nil && !types.Identical(lt, def) {
				decl = "var " + target + " " + g.typeString(lt) + " = "
			}
		}
		g.genStmt(func() {
			g.w.str(decl)
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
		g.elseInto(target, t, x.Y)
	case commaOK && declare:
		ok := g.temp("ok")
		g.genStmt(func() {
			g.w.str(target + ", " + ok + " := ")
			g.node(left)
		})
		missing := "!" + ok
		if nillable(value) {
			missing += " || " + target + " == nil"
		}
		g.w.str("if " + missing + " {\n")
		fallback()
		g.w.str("}\n")
	case commaOK:
		v, ok := g.temp("v"), g.temp("ok")
		g.w.str("if " + v + ", " + ok + " := ")
		g.node(left)
		g.w.str("; " + found(v, ok) + " {\n" + target + " = " + v + "\n")
		g.elseInto(target, t, x.Y)
	case nillable(g.typeOf(left)) && !declare && mentions(x.Y, target):
		// c = p ?? c: the fallback reads target, so p isn't stored first.
		if isRef(left) {
			v := g.renderStr(left)
			g.w.str("if " + v + " != nil {\n" + target + " = " + v + "\n")
		} else {
			v := g.temp("v")
			g.w.str("if " + v + " := ")
			g.node(left)
			g.w.str("; " + v + " != nil {\n" + target + " = " + v + "\n")
		}
		g.elseInto(target, t, x.Y)
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

// mentions reports whether x may read target, a variable or a field of
// one, as c.Name: whether it uses the variable.
func mentions(x ast.Expr, target string) bool {
	end := strings.IndexFunc(target, func(r rune) bool { return r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if end == 0 {
		return true // *p, say
	}
	if end > 0 {
		target = target[:end]
	}
	return usesName(x, target)
}

// literalType is the type := gives a literal (-1, 2.5, "s", 'c'), or nil
// for other expressions. The draft's types can't tell: there, the literal
// took the type of what it was used with.
func literalType(x ast.Expr) types.Type {
	x = ast.Unparen(x)
	if u, ok := x.(*ast.UnaryExpr); ok && (u.Op == token.SUB || u.Op == token.ADD) {
		x = u.X
	}
	lit, ok := x.(*ast.BasicLit)
	if !ok {
		return nil
	}
	switch lit.Kind {
	case token.INT:
		return types.Typ[types.Int]
	case token.FLOAT:
		return types.Typ[types.Float64]
	case token.IMAG:
		return types.Typ[types.Complex128]
	case token.CHAR:
		return types.Universe.Lookup("rune").Type()
	case token.STRING:
		return types.Typ[types.String]
	}
	return nil
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

// fstring renders an interpolated string as fmt.Sprintf, and without
// interpolation as a plain string. As an error, it's fmt.Errorf, or
// errors.New for a plain string; wrap, if set, is an error it wraps with
// ": %w".
func (g *fileGen) fstring(x *ast.FString, asError bool, wrap string) {
	raw := x.Raw()
	var format, plain strings.Builder // with % escaped for Sprintf, and as written
	var args []ast.Expr
	var specs []string
	for _, p := range x.Parts {
		if p.X == nil {
			format.WriteString(strings.ReplaceAll(p.Text, "%", "%%"))
			plain.WriteString(p.Text)
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
	switch {
	case len(args) == 0 && wrap == "" && asError:
		g.w.str(g.pkgRef("errors") + ".New(" + quote(plain.String()) + ")")
		return
	case len(args) == 0 && wrap == "":
		g.w.str(quote(plain.String()))
		return
	}
	fn := g.pkgRef("fmt") + ".Sprintf("
	if asError {
		fn = g.pkgRef("fmt") + ".Errorf("
	}
	g.w.str(fn + quote(format.String()))
	for _, a := range args {
		g.w.str(", ")
		g.node(a)
	}
	if wrap != "" {
		g.w.str(", " + wrap)
	}
	g.w.str(")")
}

// genericResults reports whether x is an argument of a call to a generic
// function, whose declaration gives the parameter x is passed as results
// that depend on type parameters: func(T) R in Map(xs []T, f func(T) R).
func (g *fileGen) genericResults(x ast.Expr) bool {
	call, ok := g.parent(x).(*ast.CallExpr)
	if !ok {
		return false
	}
	fn, ok := g.objectOf(call.Fun).(*types.Func)
	if !ok {
		return false
	}
	sig := fn.Origin().Signature()
	if sig.TypeParams().Len() == 0 {
		return false
	}
	for i, a := range call.Args {
		if a != x {
			continue
		}
		if g.leavesCtx(call, sig) {
			i++
		}
		n := sig.Params().Len()
		var t types.Type
		switch {
		case sig.Variadic() && i >= n-1:
			t = sig.Params().At(n - 1).Type().(*types.Slice).Elem()
		case i < n:
			t = sig.Params().At(i).Type()
		}
		if fs, ok := t.(*types.Signature); ok {
			return generic(fs.Results())
		}
	}
	return false
}

// boundParams returns the parameters of sig, the type of lambda x passed
// to a generic function, with the type parameters the call's other
// arguments bind: in Map(us, u => u.Name), us binds T, though nothing
// binds R yet. It returns nil if one stays unbound.
func (g *fileGen) boundParams(x *ast.LambdaExpr, sig *types.Signature) *types.Tuple {
	call, ok := g.parent(x).(*ast.CallExpr)
	if !ok {
		return nil
	}
	fn, ok := g.objectOf(call.Fun).(*types.Func)
	if !ok {
		return nil
	}
	decl := fn.Origin().Signature()
	if g.leavesCtx(call, decl) {
		return nil // an effect function: none is generic
	}
	bound := map[*types.TypeParam]types.Type{}
	for i, a := range call.Args {
		if t := g.typeOf(a); a != x && t != nil && !isUntyped(t) && i < decl.Params().Len() && !(decl.Variadic() && i == decl.Params().Len()-1) {
			unify(decl.Params().At(i).Type(), t, bound)
		}
	}
	var vs []*types.Var
	for v := range sig.Params().Variables() {
		t := subst(v.Type(), bound)
		if t == nil {
			return nil
		}
		vs = append(vs, types.NewParam(v.Pos(), v.Pkg(), v.Name(), t))
	}
	return types.NewTuple(vs...)
}

// unify binds the type parameters of p, a parameter's declared type, to
// match t, an argument's type.
func unify(p, t types.Type, bound map[*types.TypeParam]types.Type) {
	switch p := p.(type) {
	case *types.TypeParam:
		if bound[p] != nil {
			return
		}
		bound[p] = t
		// S ~[]E: []User binds E too.
		if iface, ok := p.Constraint().Underlying().(*types.Interface); ok && iface.NumEmbeddeds() == 1 {
			if u, ok := iface.EmbeddedType(0).(*types.Union); ok && u.Len() == 1 {
				unify(u.Term(0).Type(), t.Underlying(), bound)
			}
		}
	case *types.Pointer:
		if t, ok := t.Underlying().(*types.Pointer); ok {
			unify(p.Elem(), t.Elem(), bound)
		}
	case *types.Slice:
		if t, ok := t.Underlying().(*types.Slice); ok {
			unify(p.Elem(), t.Elem(), bound)
		}
	case *types.Array:
		if t, ok := t.Underlying().(*types.Array); ok {
			unify(p.Elem(), t.Elem(), bound)
		}
	case *types.Chan:
		if t, ok := t.Underlying().(*types.Chan); ok {
			unify(p.Elem(), t.Elem(), bound)
		}
	case *types.Map:
		if t, ok := t.Underlying().(*types.Map); ok {
			unify(p.Key(), t.Key(), bound)
			unify(p.Elem(), t.Elem(), bound)
		}
	case *types.Signature:
		if t, ok := t.Underlying().(*types.Signature); ok && p.Params().Len() == t.Params().Len() && p.Results().Len() == t.Results().Len() {
			for i := range p.Params().Len() {
				unify(p.Params().At(i).Type(), t.Params().At(i).Type(), bound)
			}
			for i := range p.Results().Len() {
				unify(p.Results().At(i).Type(), t.Results().At(i).Type(), bound)
			}
		}
	case *types.Named:
		if t, ok := types.Unalias(t).(*types.Named); ok && t.Origin() == p.Origin() && p.TypeArgs().Len() == t.TypeArgs().Len() {
			for i := range p.TypeArgs().Len() {
				unify(p.TypeArgs().At(i), t.TypeArgs().At(i), bound)
			}
		}
	}
}

// subst returns t with its type parameters replaced as bound gives, or nil
// if one is unbound.
func subst(t types.Type, bound map[*types.TypeParam]types.Type) types.Type {
	if !generic(t) {
		return t
	}
	switch t := t.(type) {
	case *types.TypeParam:
		return bound[t]
	case *types.Pointer:
		if e := subst(t.Elem(), bound); e != nil {
			return types.NewPointer(e)
		}
	case *types.Slice:
		if e := subst(t.Elem(), bound); e != nil {
			return types.NewSlice(e)
		}
	case *types.Array:
		if e := subst(t.Elem(), bound); e != nil {
			return types.NewArray(e, t.Len())
		}
	case *types.Chan:
		if e := subst(t.Elem(), bound); e != nil {
			return types.NewChan(t.Dir(), e)
		}
	case *types.Map:
		k, e := subst(t.Key(), bound), subst(t.Elem(), bound)
		if k != nil && e != nil {
			return types.NewMap(k, e)
		}
	case *types.Named:
		var args []types.Type
		for a := range t.TypeArgs().Types() {
			if a = subst(a, bound); a == nil {
				return nil
			}
			args = append(args, a)
		}
		if n, err := types.Instantiate(nil, t.Origin(), args, false); err == nil {
			return n
		}
	}
	return nil
}

// lambda renders x => body as a function literal, with types from where the
// lambda is used.
func (g *fileGen) lambda(x *ast.LambdaExpr) {
	f := g.pkg.fact(x)
	if g.r.ti != nil && f.sig == nil {
		if t := g.expected(x); t != nil {
			if sig, ok := t.Underlying().(*types.Signature); ok && sig.Params().Len() == countParams(x) {
				// Passed to a generic function whose type arguments aren't
				// inferred yet, as in scope.Run(ctx, s => f(s)): the
				// parameters are known, and the results come from the body.
				// Once inferred, as in Map(us, u => u.Name), results that
				// are type parameters in the declaration still come from
				// the body: the draft inferred them from a placeholder.
				switch {
				case !generic(sig) && !g.genericResults(x):
					f.sig = sig
				case !generic(sig.Params()):
					f.sig, f.openRes = sig, true
				default:
					// Map(us, u => u.Name) before R is inferred: us gives T.
					if ps := g.boundParams(x, sig); ps != nil {
						f.sig, f.openRes = types.NewSignatureType(nil, nil, nil, ps, sig.Results(), sig.Variadic()), true
					}
				}
			}
		}
	}
	if f.bodyType == nil && g.r.ti != nil {
		if b, ok := x.Body.(ast.Expr); ok {
			f.bodyType = g.typeOf(b)
		}
	}
	if b, ok := x.Body.(*ast.BlockStmt); ok && g.r.final && (f.sig == nil || f.openRes) && returnsValues(b) {
		g.errorf(x.Pos(), "can't tell the result types of this function: give it an expression body, or write a func literal")
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
		case f.sig != nil && !f.openRes:
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
		case exprBody && (f.sig == nil || f.openRes) && !g.r.final:
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
			// As in a block: the source between statements, and each on
			// its line.
			saved := g.pre
			g.pre = nil
			cur := g.off(b.Lbrace) + 1
			for _, s := range b.List {
				g.copy(cur, g.off(s.Pos()))
				g.mark(s.Pos())
				g.node(s)
				cur = g.off(s.End())
			}
			g.copy(cur, g.off(b.Rbrace))
			g.w.str("\n")
			g.pre = saved
		case ast.Expr:
			ret := "return "
			if len(resTexts) == 0 && (f.sig != nil && !f.openRes || g.r.final) {
				ret = ""
			}
			switch {
			case !g.r.final || !needsHoist(b):
				// One line, as a hand-written function literal would be.
				g.w.buf = g.w.buf[:len(g.w.buf)-1]
				g.w.str(" " + ret)
				g.node(b)
				g.w.str(" ")
			case ret != "" && len(res) == 1:
				name := g.temp("v")
				g.lowerInto(name, true, res[0], b)
				g.w.str("return " + name + "\n")
			default:
				// The statements b needs go inside the function: before
				// the statement holding it, they would run once, outside.
				g.genStmt(func() {
					g.w.str(ret)
					g.node(b)
				})
			}
		}
		g.fn = g.fn.outer
		g.w.str("}")
	})
}

// generic reports whether t mentions a type parameter: the expected type of
// an argument to a generic function, before its type arguments are inferred.
func generic(t types.Type) bool {
	switch t := t.(type) {
	case *types.TypeParam:
		return true
	case *types.Tuple:
		for v := range t.Variables() {
			if generic(v.Type()) {
				return true
			}
		}
	case *types.Signature:
		return generic(t.Params()) || generic(t.Results())
	case *types.Pointer:
		return generic(t.Elem())
	case *types.Slice:
		return generic(t.Elem())
	case *types.Array:
		return generic(t.Elem())
	case *types.Chan:
		return generic(t.Elem())
	case *types.Map:
		return generic(t.Key()) || generic(t.Elem())
	case interface{ TypeArgs() *types.TypeList }: // *types.Named, *types.Alias
		for a := range t.TypeArgs().Types() {
			if generic(a) {
				return true
			}
		}
	}
	return false
}

// returnsValues reports whether body returns values, outside the function
// literals it contains.
func returnsValues(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit, *ast.LambdaExpr:
			return false
		case *ast.ReturnStmt:
			found = found || len(n.Results) > 0
		}
		return !found
	})
	return found
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

// combinator renders all, race, retry, repeat, timeout or each as a Go call
// returning the branch values and an error.
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
		case "retry", "repeat", "timeout":
			if len(x.Args) != 2 {
				g.errorf(x.Pos(), "%s takes two arguments: %s(%s, call)", kind, kind, map[string]string{"retry": "policy", "repeat": "policy", "timeout": "duration"}[kind])
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
			switch kind {
			case "retry":
				g.w.str(g.pkgRef(schedulePath) + ".Retry(ctx, ")
			case "repeat":
				g.w.str(g.pkgRef(schedulePath) + ".Repeat(ctx, ")
			default:
				g.w.str(g.pkgRef(scopePath) + ".Timeout(ctx, ")
			}
			g.node(x.Args[0])
			g.w.str(", ")
			vals = []types.Type{g.task(x.Args[1])}
			g.w.str(")")
		case "each":
			vals = g.each(x)
		}
	})
	return vals
}

// eachLambda returns the x => call(x) of each(items, limit, x => call(x)),
// or nil if each is given a function value or isn't well formed.
func eachLambda(c *ast.CallExpr) *ast.LambdaExpr {
	if len(c.Args) != 3 {
		return nil
	}
	l, ok := c.Args[2].(*ast.LambdaExpr)
	if !ok || countParams(l) != 1 {
		return nil
	}
	if _, ok := l.Body.(ast.Expr); !ok {
		return nil
	}
	return l
}

// each renders each(items, limit, x => call(x)) as scope.Each, with the
// call in a function of its own ctx and the item. The function can also be
// a value taking (ctx, item), such as a method.
func (g *fileGen) each(x *ast.CallExpr) []types.Type {
	if len(x.Args) != 3 {
		g.errorf(x.Pos(), "each takes three arguments: each(items, limit, x => call(x))")
		g.w.str("nil")
		return nil
	}
	items, limit, f := x.Args[0], x.Args[1], x.Args[2]
	l := eachLambda(x)
	if l == nil {
		if _, ok := f.(*ast.LambdaExpr); ok {
			g.errorf(f.Pos(), "each takes a function of one item, with one call for its body: x => call(x)")
			g.w.str("nil")
			return nil
		}
		// A function value: its type gives the result.
		sig, _ := typeUnder[*types.Signature](g.typeOf(f))
		errOnly := sig != nil && sig.Results().Len() == 1 && sig.Params().Len() == 2
		switch {
		case !g.r.final && errOnly:
			g.w.str("_egoEachE(")
		case !g.r.final:
			g.w.str("_egoEachF(")
		default:
			g.w.str(g.pkgRef(scopePath) + ".Each(ctx, ")
		}
		g.node(items)
		g.w.str(", ")
		g.node(limit)
		g.w.str(", ")
		if errOnly && g.r.final {
			// scope.Each wants a value with each error: give it struct{}{}.
			if hasCall(f) {
				g.errorf(f.Pos(), "each takes a function value or x => call(x) here, not a call returning a function")
			}
			item := g.temp("item")
			g.w.str("func(ctx " + g.pkgRef("context") + ".Context, " + item + " " + g.typeString(sig.Params().At(1).Type()) + ") (struct{}, error) { return struct{}{}, ")
			g.node(f)
			g.w.str("(ctx, " + item + ") }")
		} else {
			g.node(f)
		}
		g.w.str(")")
		switch {
		case errOnly:
			return []types.Type{types.NewSlice(types.NewStruct(nil, nil))}
		case sig != nil && sig.Results().Len() == 2:
			return []types.Type{types.NewSlice(sig.Results().At(0).Type())}
		}
		return nil
	}
	body := l.Body.(ast.Expr)
	param := l.Params.List[0]
	name := param.Names[0].Name
	r := g.branchType(body) // nil: the body returns only an error
	errOnly := r == nil && g.typeOf(body) != nil
	if errOnly {
		r = types.NewStruct(nil, nil)
	}
	if !g.r.final {
		// The item comes from a range, which types it without inference.
		rs := "any"
		if r != nil {
			rs = g.typeString(r)
		}
		g.w.str("_egoEach(")
		g.node(limit)
		g.w.str(", func() " + rs + " { for _, " + name + " := range ")
		g.node(items)
		g.w.str(" { _egoUse(" + name + ")\n")
		if errOnly {
			g.w.str("_egoUse(")
			g.node(body)
			g.w.str(")\nreturn struct{}{} }\n")
		} else {
			g.w.str("return _egoV(")
			g.node(body)
			g.w.str(") }\n")
		}
		g.w.str("panic(0) })")
		if r == nil {
			return nil
		}
		return []types.Type{types.NewSlice(r)}
	}
	var elem types.Type
	if t, ok := typeUnder[*types.Slice](g.typeOf(items)); ok {
		elem = t.Elem()
	} else if t, ok := typeUnder[*types.Array](g.typeOf(items)); ok {
		elem = t.Elem()
	} else {
		g.errorf(items.Pos(), "each needs a slice or an array of items")
	}
	if r == nil {
		g.errorf(body.Pos(), "a branch must be a call returning (value, error) or error")
		r = types.NewStruct(nil, nil)
	}
	g.w.str(g.pkgRef(scopePath) + ".Each(ctx, ")
	g.node(items)
	g.w.str(", ")
	g.node(limit)
	g.w.str(", func(ctx " + g.pkgRef("context") + ".Context, ")
	g.node(param.Names[0])
	switch {
	case param.Type != nil:
		g.w.str(" ")
		g.node(param.Type)
	case elem != nil:
		g.w.str(" " + g.typeString(elem))
	}
	g.w.str(") (" + g.typeString(r) + ", error) {")
	ret := "return "
	if errOnly {
		ret += "struct{}{}, "
	}
	saved := g.fn
	g.fn = &funcState{outer: saved, ctx: true, used: map[string]bool{}, temps: map[string]bool{}}
	if saved != nil {
		g.fn.used = saved.used
		g.fn.temps = saved.temps
	}
	g.returnBody(ret, body)
	g.fn = saved
	g.w.str(")")
	return []types.Type{types.NewSlice(r)}
}

// typeUnder returns t's underlying type as a T.
func typeUnder[T types.Type](t types.Type) (T, bool) {
	var zero T
	if t == nil {
		return zero, false
	}
	u, ok := t.Underlying().(T)
	return u, ok
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
		var vals []types.Type
		g.measure(func() { vals = g.combinator(c) })
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

// measure runs fn for what it returns, discarding what it writes: its
// code, the statements it would hoist, the names it would take and its
// errors. The real rendering comes after.
func (g *fileGen) measure(fn func()) {
	savedW, savedPre, nDiags := g.w, g.pre, len(g.diags)
	var temps map[string]bool
	if g.fn != nil {
		temps = maps.Clone(g.fn.temps)
	}
	g.w = &writer{}
	if g.pre != nil {
		g.pre = &writer{}
	}
	fn()
	g.w, g.pre, g.diags = savedW, savedPre, g.diags[:nDiags]
	if g.fn != nil {
		maps.DeleteFunc(g.fn.temps, func(n string, _ bool) bool { return !temps[n] })
	}
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
	ret := "return "
	if t == nil {
		t, ret = types.NewStruct(nil, nil), "return struct{}{}, "
	}
	g.w.str("func(ctx " + ctxType + ") (" + g.typeString(t) + ", error) {")
	g.returnBody(ret, a)
	g.fn = saved
	return t
}

// returnBody writes the end of a function literal returning ret and x: on
// one line, or, when x needs statements before it, with them inside the
// function, so they run each time it does and with its ctx.
func (g *fileGen) returnBody(ret string, x ast.Expr) {
	if !g.r.final || !needsHoist(x) {
		g.w.str(" " + ret)
		g.node(x)
		g.w.str(" }")
		return
	}
	g.w.str("\n")
	g.genStmt(func() {
		g.w.str(ret)
		g.node(x)
	})
	g.w.str("}")
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
	case "retry", "repeat", "timeout":
		if len(c.Args) == 2 {
			return g.autoLabel(c.Args[1])
		}
		return ""
	case "each":
		if l := eachLambda(c); l != nil {
			return g.autoLabel(l.Body.(ast.Expr))
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
			if g.leavesCtx(p, sig) {
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
			if r == x && i < len(p.Lhs) && p.Tok != token.DEFINE {
				return g.typeOf(p.Lhs[i])
			}
		}
	case *ast.ParenExpr:
		return g.expected(p)
	case *ast.IfExpr:
		// A branch's value has the type of the whole.
		if x != p.Cond {
			return g.expected(p)
		}
	case *ast.MatchArm:
		if m, ok := g.parent(p).(*ast.MatchExpr); ok && x == p.Body {
			return g.expected(m)
		}
	case *ast.BinaryExpr:
		// An untyped operand takes the other operand's type.
		switch p.Op {
		case token.SHL, token.SHR, token.LAND, token.LOR:
			return nil
		}
		other := p.X
		if other == x {
			other = p.Y
		}
		if t := g.typeOf(other); t != nil && !isUntyped(t) && literalType(other) == nil {
			return t
		}
		if g.untyped(other) {
			return g.expected(p) // 2 * if c { 1 } else { 3 }: both untyped
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
				if lt := g.typeOf(lit); lt == nil {
					return nil // an undefined type, reported by the type check
				} else if st, ok := lt.Underlying().(*types.Struct); ok {
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
