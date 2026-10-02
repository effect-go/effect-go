package lower

import (
	"go/token"
	"go/types"
	"strings"

	"github.com/effect-go/effect-go/internal/syntax/ast"
)

// stmt renders a statement that has dialect syntax in its own expressions,
// with any statements its expressions need placed before it.
func (g *fileGen) stmt(s ast.Stmt) {
	g.w.anchor(g.off(s.Pos()))
	savedW, savedPre := g.w, g.pre
	pre, body := &writer{}, &writer{}
	g.w, g.pre = body, pre
	g.lowerStmt(s)
	g.w, g.pre = savedW, savedPre
	g.w.add(pre)
	g.w.add(body)
}

func (g *fileGen) lowerStmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.AssignStmt:
		if len(s.Rhs) == 1 {
			switch x := s.Rhs[0].(type) {
			case *ast.CheckExpr:
				g.check(s.Lhs, s.Tok, x)
				return
			case *ast.ElseExpr:
				g.elseStmt(s.Lhs, s.Tok, x)
				return
			case *ast.IfExpr, *ast.MatchExpr, *ast.CoalesceExpr, *ast.OptSelectorExpr:
				if g.r.final && len(s.Lhs) == 1 && (s.Tok == token.DEFINE || s.Tok == token.ASSIGN) {
					g.lowerInto(g.renderStr(s.Lhs[0]), s.Tok == token.DEFINE, g.typeOf(x), x)
					g.trimNewline()
					return
				}
			}
		}
	case *ast.ExprStmt:
		if c, ok := s.X.(*ast.CheckExpr); ok {
			g.check(nil, token.ILLEGAL, c)
			return
		}
	case *ast.FailStmt:
		g.fail(s)
		return
	case *ast.MatchStmt:
		g.matchStmt(s)
		return
	case *ast.IfStmt:
		if s.Init != nil && hasDirectCheck(s.Init) {
			g.errorf(s.Init.Pos(), "check can't be used in an if header (nor else or must): write it on its own line before the if")
		}
		g.ifStmt(s)
		return
	case *ast.ForStmt:
		if needsHoist(s.Cond) || needsHoist(s.Post) {
			g.errorf(s.Pos(), "this expression can't be used in a for header: assign it to a variable in the loop")
		}
	case *ast.SwitchStmt:
		for _, c := range s.Body.List {
			for _, e := range c.(*ast.CaseClause).List {
				if needsHoist(e) {
					g.errorf(e.Pos(), "this expression can't be used in a case: assign it to a variable first")
				}
			}
		}
	}
	g.children(s)
}

// ifStmt renders an if statement. A hoisted expression in an else-if
// condition must not run before the earlier conditions, so that else
// branch becomes a block.
func (g *fileGen) ifStmt(s *ast.IfStmt) {
	e, ok := s.Else.(*ast.IfStmt)
	if !ok || !g.r.final || !(needsHoist(e.Cond) || e.Init != nil && needsHoist(e.Init)) {
		g.children(s)
		return
	}
	cur := g.off(s.Pos())
	for _, c := range []ast.Node{s.Init, s.Cond, s.Body} {
		if c == nil || c == ast.Stmt(nil) {
			continue
		}
		if st, ok := c.(ast.Stmt); ok && st == nil {
			continue
		}
		g.copy(cur, g.off(c.Pos()))
		g.node(c)
		cur = g.off(c.End())
	}
	g.w.str(" else {\n")
	g.mark(e.Pos())
	g.stmt(e)
	g.w.str("\n}")
}

func hasDirectCheck(s ast.Stmt) bool {
	found := false
	ast.Inspect(s, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.CheckExpr, *ast.ElseExpr:
			found = true
		case *ast.FuncLit, *ast.LambdaExpr:
			return false
		}
		return !found
	})
	return found
}

// trimNewline drops a trailing newline, since the caller's source supplies
// one.
func (g *fileGen) trimNewline() {
	if n := len(g.w.buf); n > 0 && g.w.buf[n-1] == '\n' {
		g.w.buf = g.w.buf[:n-1]
	}
}

// renderStr renders a node into a string.
func (g *fileGen) renderStr(n ast.Node) string {
	saved := g.w
	g.w = &writer{}
	g.node(n)
	s := string(g.w.buf)
	g.w = saved
	return s
}

// value renders the call a check, else or must applies to, and returns the
// types of its results, the error excluded.
func (g *fileGen) value(x ast.Expr) (vals []types.Type, hasErr, known bool) {
	if c, ok := ast.Unparen(x).(*ast.CallExpr); ok && g.builtin(c) != "" {
		vals = g.combinator(c)
		var out []types.Type
		for _, v := range vals {
			out = append(out, v)
		}
		return out, true, true
	}
	if _, ok := ast.Unparen(x).(*ast.CallExpr); !ok {
		g.errorf(x.Pos(), "expected a call")
	}
	g.node(x)
	rs := results(g.typeOf(x))
	if rs == nil {
		return nil, true, false
	}
	if n := len(rs); n > 0 && implementsError(rs[n-1]) {
		return rs[:n-1], true, true
	}
	return rs, false, true
}

// check lowers check and must.
func (g *fileGen) check(lhs []ast.Expr, tok token.Token, c *ast.CheckExpr) {
	if !g.r.final {
		g.draftCheck(lhs, tok, c)
		return
	}
	if !c.Must && (g.fn == nil || !g.fn.hasErr) {
		g.errorf(c.Pos(), "check needs a function that returns an error: use must, or else")
	}
	if _, isCall := ast.Unparen(c.X).(*ast.CallExpr); !isCall && lhs == nil {
		g.checkValue(c)
		return
	}
	// Render the call first, to learn its result types.
	call := &writer{}
	saved := g.w
	g.w = call
	vals, hasErr, known := g.value(c.X)
	g.w = saved
	if known && !hasErr {
		g.errorf(c.X.Pos(), "check needs a call that returns an error")
	}
	if known && lhs != nil && len(lhs) != len(vals) {
		g.errorf(c.Pos(), "%d variables but the call returns %d values and an error", len(lhs), len(vals))
	}
	n := len(vals)
	if lhs != nil {
		n = len(lhs)
	}

	// What to do with the error.
	var handle string
	switch {
	case c.Must:
		handle = "panic(err)"
	default:
		handle = g.ret(g.wrap(c, "err"))
	}

	switch {
	case lhs == nil:
		blanks := strings.Repeat("_, ", n)
		g.w.str("if " + blanks + "err := ")
		g.w.add(call)
		g.w.str("; err != nil {\n" + handle + "\n}")
	case tok == token.DEFINE:
		g.lhs(lhs)
		g.w.str(", err := ")
		g.w.add(call)
		g.w.str("\nif err != nil {\n" + handle + "\n}")
	default:
		var tmps []string
		for range lhs {
			tmps = append(tmps, g.temp("v"))
		}
		g.w.str(strings.Join(tmps, ", ") + ", err := ")
		g.w.add(call)
		g.w.str("\nif err != nil {\n" + handle + "\n}\n")
		g.lhs(lhs)
		g.w.str(" " + tok.String() + " " + strings.Join(tmps, ", "))
	}
}

func (g *fileGen) lhs(lhs []ast.Expr) {
	for i, l := range lhs {
		if i > 0 {
			g.w.str(", ")
		}
		g.node(l)
	}
}

// draftCheck renders check, must and the call of else for a draft.
func (g *fileGen) draftCheck(lhs []ast.Expr, tok token.Token, c *ast.CheckExpr) {
	switch {
	case c.Case == nil:
	case isCompositeLit(c.Case):
		g.w.str("_egoUse(")
		g.node(c.Case)
		g.w.str(")\n")
	default:
		g.w.str("_egoUse(*new(")
		g.w.rec(c.Case, func() { g.node(c.Case) })
		g.w.str("))\n")
	}
	x := c.X
	if lhs == nil {
		g.w.str("_egoUse(")
		g.value(x)
		g.w.str(")")
		return
	}
	g.lhs(lhs)
	g.w.str(", _ " + tok.String() + " ")
	g.value(x)
}

// wrap returns the error a check returns, given the variable holding the
// error: labelled, or as an error set case.
func (g *fileGen) wrap(c *ast.CheckExpr, err string) string {
	set := (*ast.SumDecl)(nil)
	if g.fn != nil {
		set = g.fn.set
	}
	if c.Case != nil {
		lit, _ := c.Case.(*ast.CompositeLit)
		typ := c.Case
		if lit != nil {
			typ = lit.Type
		}
		t := g.typeOf(c.Case)
		field := ""
		if t == nil {
			// A case of a set declared in this package.
			for _, sd := range g.pkg.sums {
				for _, sc := range sd.Cases {
					if sc.Name.Name == lastName(typ) {
						field = causeField(sc)
					}
				}
			}
		}
		if t != nil {
			if st, ok := t.Underlying().(*types.Struct); ok {
				for f := range st.Fields() {
					if isError(f.Type()) {
						field = f.Name()
						break
					}
				}
			}
			if field == "" && lit == nil {
				g.errorf(c.Case.Pos(), "%s has no error field to hold the cause: give its fields, as in %s{…}", g.text(typ), g.text(typ))
			}
			if set != nil && !g.inSet(t, set) {
				g.errorf(c.Case.Pos(), "%s is not a case of %s", g.text(typ), set.Name.Name)
			}
		}
		if lit == nil {
			return g.renderStr(c.Case) + "{" + field + ": " + err + "}"
		}
		// as Case{…}: the cause goes in the error field, unless set.
		v := g.renderStr(lit)
		if field == "" || hasKey(lit, field) {
			return v
		}
		for _, e := range lit.Elts {
			if _, keyed := e.(*ast.KeyValueExpr); !keyed {
				g.errorf(e.Pos(), "name the fields of %s, so the cause can be added", g.text(typ))
				return v
			}
		}
		sep := ""
		if len(lit.Elts) > 0 {
			sep = ", "
		}
		return strings.TrimSuffix(v, "}") + sep + field + ": " + err + "}"
	}
	if set != nil {
		if !g.passesSet(c.X, set) {
			g.errorf(c.Pos(), "this function returns %s: write check … as <case>", set.Name.Name)
		}
		return err
	}
	if c.Label != nil {
		saved := g.w
		g.w = &writer{}
		g.fstring(c.Label, err, nil)
		s := string(g.w.buf)
		g.w = saved
		return s
	}
	label := g.autoLabel(c.X)
	if label == "" {
		return err
	}
	return g.pkgRef("fmt") + `.Errorf("` + label + `: %w", ` + err + `)`
}

// inSet reports whether a case type belongs to an error set.
func (g *fileGen) inSet(t types.Type, set *ast.SumDecl) bool {
	s := setOf(t)
	return s != nil && s.Obj().Name() == set.Name.Name
}

// passesSet reports whether a call returns the same error set, so its
// error can be passed on as it is.
func (g *fileGen) passesSet(x ast.Expr, set *ast.SumDecl) bool {
	c, ok := ast.Unparen(x).(*ast.CallExpr)
	if !ok {
		return false
	}
	switch g.builtin(c) {
	case "all", "race":
		for _, a := range c.Args {
			if !g.passesSet(a, set) {
				return false
			}
		}
		return true
	case "retry", "timeout":
		return len(c.Args) == 2 && g.passesSet(c.Args[1], set)
	}
	var name string
	switch f := c.Fun.(type) {
	case *ast.Ident:
		name = f.Name
	case *ast.SelectorExpr:
		name = f.Sel.Name
		if t := g.typeOf(f.X); t != nil {
			if p, ok := t.(*types.Pointer); ok {
				t = p.Elem()
			}
			if n, ok := types.Unalias(t).(*types.Named); ok {
				name = n.Obj().Name() + "." + name
			}
		}
	}
	return g.pkg.setFuncs[name] == set
}

// elseStmt lowers x := f() else fallback.
func (g *fileGen) elseStmt(lhs []ast.Expr, tok token.Token, e *ast.ElseExpr) {
	if !g.r.final {
		g.lhs(lhs)
		g.w.str(" " + tok.String() + " _egoCo(_egoV(")
		g.value(e.X)
		g.w.str("), ")
		g.node(e.Fallback)
		g.w.str(")")
		return
	}
	if len(lhs) != 1 {
		g.errorf(e.Pos(), "else needs a single variable")
	}
	call := &writer{}
	saved := g.w
	g.w = call
	vals, hasErr, known := g.value(e.X)
	g.w = saved
	if known && (!hasErr || len(vals) != 1) {
		g.errorf(e.X.Pos(), "else needs a call returning (value, error)")
	}
	var t types.Type
	if len(vals) == 1 {
		t = vals[0]
	}
	target := g.renderStr(lhs[0])
	if tok == token.DEFINE {
		g.w.str(target + ", err := ")
		g.w.add(call)
		g.w.str("\nif err != nil {\n")
		g.lowerInto(target, false, t, e.Fallback)
		g.w.str("}")
		return
	}
	v := g.temp("v")
	g.w.str("if " + v + ", err := ")
	g.w.add(call)
	g.w.str("; err != nil {\n")
	g.lowerInto(target, false, t, e.Fallback)
	g.w.str("} else {\n" + target + " = " + v + "\n}")
}

// fail lowers fail X.
func (g *fileGen) fail(s *ast.FailStmt) {
	if g.fn == nil || !g.fn.hasErr {
		g.errorf(s.Pos(), "fail needs a function that returns an error")
	}
	var errText string
	switch x := s.X.(type) {
	case *ast.FString:
		saved := g.w
		g.w = &writer{}
		hasArgs := false
		for _, p := range x.Parts {
			if p.X != nil {
				hasArgs = true
			}
		}
		if hasArgs {
			g.fstring(x, "", nil)
			errText = strings.Replace(string(g.w.buf), ".Sprintf(", ".Errorf(", 1)
		} else {
			g.fstring(x, "", nil)
			errText = g.pkgRef("errors") + ".New(" + string(g.w.buf) + ")"
		}
		g.w = saved
		if g.fn != nil && g.fn.set != nil {
			g.errorf(s.X.Pos(), "this function returns %s: fail with one of its cases", g.fn.set.Name.Name)
		}
	default:
		if g.r.final && g.fn != nil && g.fn.set != nil {
			if t := g.typeOf(x); t != nil && !g.inSet(t, g.fn.set) && !isErrorSet(t, g.fn.set) {
				g.errorf(x.Pos(), "%s is not a case of %s", types.TypeString(t, func(*types.Package) string { return "" }), g.fn.set.Name.Name)
			}
		}
		g.w.str(strings.TrimSuffix(g.ret("X"), "X"))
		g.node(x)
		return
	}
	g.w.str(g.ret(errText))
}

func isErrorSet(t types.Type, set *ast.SumDecl) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Name() == set.Name.Name
}

// checkValue lowers check err, for an error value: return it if it isn't
// nil, with a custom label if there is one.
func (g *fileGen) checkValue(c *ast.CheckExpr) {
	if !isSimple(c.X) {
		g.errorf(c.X.Pos(), "check needs a call or a variable")
	}
	if t := g.typeOf(c.X); t != nil && !implementsError(t) {
		g.errorf(c.X.Pos(), "check needs an error")
	}
	v := g.renderStr(c.X)
	g.w.str("if ")
	g.node(c.X)
	g.w.str(" != nil {\n")
	var handle string
	switch {
	case c.Must:
		handle = "panic(" + v + ")"
	default:
		handle = g.ret(g.wrap(c, v))
	}
	g.w.str(handle + "\n}")
}

func isCompositeLit(x ast.Expr) bool {
	_, ok := x.(*ast.CompositeLit)
	return ok
}

// hasKey reports whether a composite literal sets the field name.
func hasKey(lit *ast.CompositeLit, name string) bool {
	for _, e := range lit.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			if id, ok := kv.Key.(*ast.Ident); ok && id.Name == name {
				return true
			}
		}
	}
	return false
}
