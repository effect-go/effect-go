// Package fiberjoin finds fibers started with scope.Fork that are never
// joined, interrupted or waited on. Such a fiber's error is lost, and its
// panic re-panics when the scope closes.
package fiberjoin

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

var Analyzer = &analysis.Analyzer{
	Name:     "fiberjoin",
	Doc:      "check that fibers started with scope.Fork are joined",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

const scopePath = "github.com/effect-go/effect-go/scope"

func run(pass *analysis.Pass) (any, error) {
	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	fibers := map[types.Object]*ast.Ident{} // variables holding a fiber
	ins.Preorder([]ast.Node{(*ast.ExprStmt)(nil), (*ast.AssignStmt)(nil), (*ast.ValueSpec)(nil)}, func(n ast.Node) {
		switch n := n.(type) {
		case *ast.ValueSpec: // var f = scope.Fork(…)
			if len(n.Names) == 1 && len(n.Values) == 1 && isFork(pass, n.Values[0]) {
				if n.Names[0].Name == "_" {
					pass.Reportf(n.Pos(), "the fiber is never joined: its error is lost")
				} else if obj := pass.TypesInfo.Defs[n.Names[0]]; obj != nil {
					fibers[obj] = n.Names[0]
				}
			}
		case *ast.ExprStmt:
			if isFork(pass, n.X) {
				pass.Reportf(n.Pos(), "the fiber is never joined: its error is lost")
			}
		case *ast.AssignStmt:
			if len(n.Rhs) != 1 || len(n.Lhs) != 1 || !isFork(pass, n.Rhs[0]) {
				return
			}
			id, ok := n.Lhs[0].(*ast.Ident)
			if !ok {
				return
			}
			if id.Name == "_" {
				pass.Reportf(n.Pos(), "the fiber is never joined: its error is lost")
				return
			}
			if obj := pass.TypesInfo.ObjectOf(id); obj != nil {
				fibers[obj] = id
			}
		}
	})
	// A fiber is handled if it's joined, interrupted or waited on, or
	// handed to other code that may do it.
	handled := map[types.Object]bool{}
	ins.WithStack([]ast.Node{(*ast.Ident)(nil)}, func(n ast.Node, push bool, stack []ast.Node) bool {
		obj := pass.TypesInfo.Uses[n.(*ast.Ident)]
		if obj == nil || fibers[obj] == nil || len(stack) < 2 {
			return true
		}
		switch p := stack[len(stack)-2].(type) {
		case *ast.SelectorExpr:
			switch p.Sel.Name {
			case "Join", "Interrupt", "Done":
				handled[obj] = true
			}
		case *ast.CallExpr, *ast.ReturnStmt, *ast.CompositeLit, *ast.KeyValueExpr, *ast.SendStmt:
			handled[obj] = true
		case *ast.AssignStmt:
			for _, r := range p.Rhs {
				if r == n {
					handled[obj] = true
				}
			}
		}
		return true
	})
	for obj, id := range fibers {
		if !handled[obj] {
			pass.Reportf(id.Pos(), "fiber %s is never joined, interrupted or waited on", id.Name)
		}
	}
	return nil, nil
}

func isFork(pass *analysis.Pass, x ast.Expr) bool {
	call, ok := x.(*ast.CallExpr)
	if !ok {
		return false
	}
	fun := call.Fun
	switch f := fun.(type) {
	case *ast.IndexExpr:
		fun = f.X
	case *ast.IndexListExpr:
		fun = f.X
	}
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	f, ok := pass.TypesInfo.ObjectOf(sel.Sel).(*types.Func)
	return ok && f.Pkg() != nil && f.Pkg().Path() == scopePath && f.Name() == "Fork"
}
