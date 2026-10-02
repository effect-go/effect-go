// Package exhaustive checks that switches over closed sets handle every
// case. A default clause opts out.
//
//   - Type switches over a sum type: an interface sealed by an unexported
//     marker method is<Name>, the convention error sets and enums compile to.
//   - Switches over a named type with constants (Go's usual enum) that
//     already handle at least half of them: a switch on a few values of a
//     large set, such as token kinds, is ordinary Go.
package exhaustive

import (
	"go/ast"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

var Analyzer = &analysis.Analyzer{
	Name:     "exhaustive",
	Doc:      "check that switches over sealed interfaces and enum constants handle every case",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (any, error) {
	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	ins.Preorder([]ast.Node{(*ast.TypeSwitchStmt)(nil), (*ast.SwitchStmt)(nil)}, func(n ast.Node) {
		switch s := n.(type) {
		case *ast.TypeSwitchStmt:
			typeSwitch(pass, s)
		case *ast.SwitchStmt:
			constSwitch(pass, s)
		}
	})
	return nil, nil
}

func hasDefault(body *ast.BlockStmt) bool {
	for _, c := range body.List {
		if c.(*ast.CaseClause).List == nil {
			return true
		}
	}
	return false
}

func typeSwitch(pass *analysis.Pass, s *ast.TypeSwitchStmt) {
	if hasDefault(s.Body) {
		return
	}
	var x ast.Expr
	switch a := s.Assign.(type) {
	case *ast.AssignStmt:
		x = a.Rhs[0].(*ast.TypeAssertExpr).X
	case *ast.ExprStmt:
		x = a.X.(*ast.TypeAssertExpr).X
	}
	t := pass.TypesInfo.TypeOf(x)
	named, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return
	}
	iface, ok := named.Underlying().(*types.Interface)
	if !ok || !sealed(iface, named.Obj().Name()) || named.Obj().Pkg() == nil {
		return
	}
	covered := map[types.Type]bool{}
	for _, c := range s.Body.List {
		for _, e := range c.(*ast.CaseClause).List {
			if ct := pass.TypesInfo.TypeOf(e); ct != nil {
				covered[ct] = true
			}
		}
	}
	var missing []string
	scope := named.Obj().Pkg().Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() || types.IsInterface(tn.Type()) {
			continue
		}
		for _, ct := range []types.Type{tn.Type(), types.NewPointer(tn.Type())} {
			if !types.Implements(ct, iface) {
				continue
			}
			found := false
			for c := range covered {
				if types.Identical(c, ct) {
					found = true
				}
			}
			if !found {
				missing = append(missing, types.TypeString(ct, types.RelativeTo(pass.Pkg)))
			}
			break
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		pass.Reportf(s.Pos(), "type switch on %s doesn't handle %s", named.Obj().Name(), strings.Join(missing, ", "))
	}
}

// sealed reports whether iface has the marker method is<name>, so only its
// own package implements it.
func sealed(iface *types.Interface, name string) bool {
	for m := range iface.Methods() {
		if m.Name() == "is"+name {
			return true
		}
	}
	return false
}

func constSwitch(pass *analysis.Pass, s *ast.SwitchStmt) {
	if s.Tag == nil || hasDefault(s.Body) {
		return
	}
	named, ok := types.Unalias(pass.TypesInfo.TypeOf(s.Tag)).(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return
	}
	if _, ok := named.Underlying().(*types.Basic); !ok {
		return
	}
	var consts []*types.Const
	scope := named.Obj().Pkg().Scope()
	for _, name := range scope.Names() {
		if c, ok := scope.Lookup(name).(*types.Const); ok && types.Identical(c.Type(), named) {
			consts = append(consts, c)
		}
	}
	if len(consts) < 2 {
		return
	}
	sort.Slice(consts, func(i, j int) bool { return consts[i].Pos() < consts[j].Pos() })
	covered := map[string]bool{}
	for _, c := range s.Body.List {
		for _, e := range c.(*ast.CaseClause).List {
			if tv, ok := pass.TypesInfo.Types[e]; ok && tv.Value != nil {
				covered[tv.Value.ExactString()] = true
			}
		}
	}
	var missing []string
	for _, c := range consts {
		if !covered[c.Val().ExactString()] {
			missing = append(missing, c.Name())
			covered[c.Val().ExactString()] = true // aliases of one value count once
		}
	}
	if len(missing) > 0 && len(missing)*2 <= len(consts) {
		pass.Reportf(s.Pos(), "switch on %s doesn't handle %s", named.Obj().Name(), strings.Join(missing, ", "))
	}
}
