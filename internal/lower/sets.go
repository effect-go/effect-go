package lower

import (
	"go/token"
	"go/types"
	"slices"

	"github.com/effect-go/effect-go/internal/syntax/ast"
)

// A function that returns an error set returns only its cases, so a match
// on its errors needs no _ arm. These are the checks that keep it so.

// callSet returns the error set every error of a call belongs to: the
// callee is declared in .ego to return it, or is all or race of calls that
// do, or retry or repeat of one. timeout and each can fail with a
// cancellation of their own, so they pass no set on.
func (g *fileGen) callSet(c *ast.CallExpr) *types.Named {
	switch g.builtin(c) {
	case "all", "race":
		var set *types.Named
		for i, a := range c.Args {
			s := g.argSet(a)
			if s == nil || i > 0 && !sameSet(s, set) {
				return nil
			}
			set = s
		}
		return set
	case "retry", "repeat":
		if len(c.Args) == 2 {
			return g.argSet(c.Args[1])
		}
		return nil
	case "timeout", "each":
		return nil
	}
	return g.funcSet(c.Fun)
}

// funcSet returns the error set a function or method returns, if it is
// declared in .ego to return one.
func (g *fileGen) funcSet(fun ast.Expr) *types.Named {
	pkg, sig := g.sig(fun)
	if sig == nil || sig.result == nil {
		return nil
	}
	return lookupSet(pkg, sig.result)
}

// sig returns the package of a called function or method, and the error
// sets in its signature, if it is declared in .ego with any.
func (g *fileGen) sig(fun ast.Expr) (*types.Package, *setSig) {
	pkg, key := g.callee(fun)
	switch {
	case pkg == nil:
		return nil, nil
	case pkg.Path() == g.pkg.path:
		return pkg, g.pkg.sigs[key]
	case g.pkg.under != nil && pkg.Path() == g.pkg.under.path:
		return pkg, g.pkg.under.sigs[key]
	}
	return pkg, g.pkg.cfg.Importer.setSigs(pkg.Path())[key]
}

// lookupSet returns the type of an error set a signature of pkg names: declared
// in pkg, or in a package it imports.
func lookupSet(pkg *types.Package, set *errSet) *types.Named {
	if pkg.Path() != set.path {
		i := slices.IndexFunc(pkg.Imports(), func(p *types.Package) bool { return p.Path() == set.path })
		if i < 0 {
			return nil
		}
		pkg = pkg.Imports()[i]
	}
	if tn, ok := pkg.Scope().Lookup(set.name).(*types.TypeName); ok {
		n, _ := types.Unalias(tn.Type()).(*types.Named)
		return n
	}
	return nil
}

// argSet is callSet for an expression that may not be a call.
func (g *fileGen) argSet(x ast.Expr) *types.Named {
	if c, ok := ast.Unparen(x).(*ast.CallExpr); ok {
		return g.callSet(c)
	}
	return nil
}

// callee returns the package of a called function or method, and its key
// in sigs: "Name" or "Recv.Name".
func (g *fileGen) callee(fun ast.Expr) (*types.Package, string) {
	switch f := ast.Unparen(fun).(type) {
	case *ast.Ident, *ast.SelectorExpr:
		fn, ok := g.objectOf(f).(*types.Func)
		if !ok || fn.Pkg() == nil {
			return nil, ""
		}
		recv := fn.Signature().Recv()
		if recv == nil {
			return fn.Pkg(), fn.Name()
		}
		t := recv.Type()
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
		}
		if n, ok := types.Unalias(t).(*types.Named); ok {
			return fn.Pkg(), n.Obj().Name() + "." + fn.Name()
		}
	}
	return nil, ""
}

func sameSet(a, b *types.Named) bool {
	return a != nil && b != nil && a.Obj().Name() == b.Obj().Name() && a.Obj().Pkg().Path() == b.Obj().Pkg().Path()
}

// isCheck reports whether x is a check, misplaced if it is a result.
func isCheck(x ast.Expr) bool {
	_, ok := x.(*ast.CheckExpr)
	return ok
}

// passesSet reports whether x is a call whose errors are all of set, so
// they can be passed on as they are.
func (g *fileGen) passesSet(x ast.Expr, set *errSet) bool { return isErrorSet(g.argSet(x), set) }

// isErrorSet reports whether t is the interface type of set.
func isErrorSet(t types.Type, set *errSet) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n != nil && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == set.path && n.Obj().Name() == set.name
}

// inSet reports whether t is a case of set.
func inSet(t types.Type, set *errSet) bool {
	s := setOf(t)
	return s != nil && isErrorSet(s, set)
}

// checkSets reports the errors that may not be cases of the error set
// they are given as: returned by a function that returns a set, or passed
// as a parameter declared as one.
func (g *fileGen) checkSets() {
	ast.Inspect(g.file, func(n ast.Node) bool {
		var ft *ast.FuncType
		var body *ast.BlockStmt
		switch n := n.(type) {
		case *ast.FuncDecl:
			ft, body = n.Type, n.Body
		case *ast.FuncLit:
			ft, body = n.Type, n.Body
		case *ast.CallExpr:
			g.checkArgs(n)
			return true
		default:
			return true
		}
		set := g.resultSet(ft)
		if set == nil || body == nil {
			return true
		}
		results := 0
		for _, f := range ft.Results.List {
			results += max(1, len(f.Names))
		}
		// A named result is returned by a bare return.
		if names := ft.Results.List[len(ft.Results.List)-1].Names; len(names) > 0 {
			if _, why := g.varSet(names[len(names)-1]); why != "" {
				g.errorf(names[len(names)-1].Pos(), "%s can be given errors that aren't cases of %s (%s)", names[len(names)-1].Name, set.text, why)
			}
		}
		ast.Inspect(body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncLit:
				return false // checked on its own
			case *ast.ReturnStmt:
				if len(n.Results) == results {
					g.returnsCase(n.Results[results-1], set)
				} else if len(n.Results) == 1 && !isCheck(n.Results[0]) && !g.passesSet(n.Results[0], set) {
					g.errorf(n.Results[0].Pos(), "%s may fail with errors that aren't cases of %s: use check … as <case>", g.text(n.Results[0]), set.text)
				}
			}
			return true
		})
		return true
	})
}

// checkArgs reports the arguments of a call that may not be cases of the
// error sets their parameters are declared as.
func (g *fileGen) checkArgs(c *ast.CallExpr) {
	if g.builtin(c) != "" {
		return
	}
	pkg, sig := g.sig(c.Fun)
	if sig == nil || len(sig.params) == 0 {
		return
	}
	// A call from plain Go to an effect function passes its ctx too.
	extra := len(c.Args) - sig.nparams
	if extra != 0 && extra != 1 {
		return
	}
	for i, set := range sig.params {
		arg := c.Args[i+extra]
		if g.isNil(arg) {
			continue
		}
		if src, why := g.errSource(arg); !sameSet(src, lookupSet(pkg, set)) {
			if why != "" {
				why = " (" + why + ")"
			}
			g.errorf(arg.Pos(), "%s can hold errors that aren't cases of %s%s, and %s takes only %s", g.text(arg), set.text, why, g.text(c.Fun), set.text)
		}
	}
}

// returnsCase reports x, returned as an error of set, unless it is nil, a
// case of set or a call returning set.
func (g *fileGen) returnsCase(x ast.Expr, set *errSet) {
	switch x := ast.Unparen(x).(type) {
	case *ast.IfExpr:
		g.returnsCase(x.Then, set)
		g.returnsCase(x.Else, set)
		return
	case *ast.MatchExpr:
		for _, a := range x.Arms {
			if e, ok := a.Body.(ast.Expr); ok {
				g.returnsCase(e, set)
			}
		}
		return
	}
	if g.isNil(x) || g.passesSet(x, set) {
		return
	}
	t := g.typeOf(x)
	if t == nil || inSet(t, set) || isErrorSet(t, set) {
		return
	}
	g.errorf(x.Pos(), "%s is not a case of %s: return one of its cases, or use check … as <case>", g.text(x), set.text)
}

func (g *fileGen) isNil(x ast.Expr) bool {
	id, ok := ast.Unparen(x).(*ast.Ident)
	if !ok || id.Name != "nil" {
		return false
	}
	obj := g.objectOf(id)
	_, isNil := obj.(*types.Nil)
	return isNil || obj == nil
}

// errSource returns the error set every error x can hold belongs to: x is
// a call returning the set, a value of the set, or a local variable only
// ever assigned those and nil. Otherwise it returns nil and, if it can
// tell, where other errors would come from.
func (g *fileGen) errSource(x ast.Expr) (*types.Named, string) {
	x = ast.Unparen(x)
	if s := g.valueSet(x); s != nil {
		return s, ""
	}
	switch x := x.(type) {
	case *ast.CallExpr:
		return nil, "it comes from " + g.text(x.Fun)
	case *ast.Ident:
		return g.varSet(x)
	}
	return nil, ""
}

// valueSet returns the error set of a call returning one, or of a value
// of a case or of the set's type.
func (g *fileGen) valueSet(x ast.Expr) *types.Named {
	if s := g.argSet(x); s != nil {
		return s
	}
	t := g.typeOf(x)
	if t == nil || !implementsError(t) {
		return nil
	}
	if s := setOf(t); s != nil {
		return s
	}
	s, _ := sumCases(t)
	return s
}

// varSet is errSource for a variable: every assignment to it, in the
// function that declares it, must give nil or an error of the same set.
func (g *fileGen) varSet(id *ast.Ident) (*types.Named, string) {
	v, ok := g.objectOf(id).(*types.Var)
	if !ok {
		return nil, ""
	}
	if v.Pkg() != nil && v.Parent() == v.Pkg().Scope() {
		return nil, "it's a package variable"
	}
	var set *types.Named
	why := ""
	add := func(s *types.Named) {
		if set != nil && !sameSet(s, set) {
			why = "it's given errors of " + set.Obj().Name() + " and " + s.Obj().Name()
		}
		set = s
	}
	assigned := func(x ast.Expr) {
		if why != "" || g.isNil(x) {
			return
		}
		if s := g.valueSet(x); s != nil {
			add(s)
		} else if c, ok := ast.Unparen(x).(*ast.CallExpr); ok {
			why = "it comes from " + g.text(c.Fun)
		} else {
			why = "it's assigned " + g.text(x)
		}
	}
	// lhs and rhs are an assignment's sides; ref is one of lhs.
	assign := func(lhs, rhs []ast.Expr, ref ast.Expr) {
		for i, l := range lhs {
			switch {
			case l != ref:
			case len(rhs) == len(lhs):
				assigned(rhs[i])
			case i == len(lhs)-1 && len(rhs) == 1:
				assigned(rhs[0]) // x, err := f()
			default:
				why = "it's assigned " + g.text(rhs[0])
			}
		}
	}
	ast.Inspect(g.outermostFunc(id), func(n ast.Node) bool {
		ref, ok := n.(*ast.Ident)
		if why != "" || !ok || ref.Name != v.Name() || g.objectOf(ref) != v {
			return why == ""
		}
		switch p := g.parent(ref).(type) {
		case *ast.Field:
			fl, _ := g.parent(p).(*ast.FieldList)
			ft, _ := g.parent(fl).(*ast.FuncType)
			switch ps := g.namedSet(p.Type); {
			case ps != nil && g.r.ti != nil:
				add(lookupSet(g.r.ti.pkg, ps))
			case ft != nil && ft.Results == fl:
				why = "it's a named result"
			default:
				why = whyParam
			}
		case *ast.UnaryExpr:
			if p.Op == token.AND {
				why = "its address is taken"
			}
		case *ast.AssignStmt:
			assign(p.Lhs, p.Rhs, ref)
		case *ast.ValueSpec:
			names := make([]ast.Expr, len(p.Names))
			for i, n := range p.Names {
				names[i] = n
			}
			if len(p.Values) > 0 {
				assign(names, p.Values, ref)
			}
		case *ast.RangeStmt:
			if p.Key == ref || p.Value == ref {
				why = "a range assigns it"
			}
		}
		return why == ""
	})
	if why != "" {
		return nil, why
	}
	return set, ""
}

const whyParam = "it's a parameter"

// outermostFunc returns the outermost function declaration or literal
// that contains n, or n's file.
func (g *fileGen) outermostFunc(n ast.Node) ast.Node {
	var out ast.Node = g.file
	for p := g.parent(n); p != nil; p = g.parent(p) {
		switch p.(type) {
		case *ast.FuncDecl, *ast.FuncLit:
			out = p
		}
	}
	return out
}
