// Package ctxcheck finds child tasks that use their parent's context
// instead of their own, so they aren't cancelled when a sibling fails:
//
//   - in a function passed to (*errgroup.Group).Go, a use of the context
//     the group was derived from, rather than the one WithContext returned;
//   - in a task passed to the effect-go scope or schedule packages, a use of
//     an outer context.Context instead of the task's ctx parameter.
package ctxcheck

import (
	"github.com/effect-go/effect-go/internal/typeutil"
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

var Analyzer = &analysis.Analyzer{
	Name:     "ctxcheck",
	Doc:      "check that child tasks use their own context",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

const (
	errgroupPath = "golang.org/x/sync/errgroup"
	scopePath    = "github.com/effect-go/effect-go/scope"
	schedulePath = "github.com/effect-go/effect-go/schedule"
)

func run(pass *analysis.Pass) (any, error) {
	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	parentOf := map[types.Object]types.Object{} // group -> context it was derived from
	ins.Preorder([]ast.Node{(*ast.AssignStmt)(nil), (*ast.CallExpr)(nil)}, func(n ast.Node) {
		switch n := n.(type) {
		case *ast.AssignStmt:
			// g, gctx := errgroup.WithContext(ctx)
			if len(n.Lhs) != 2 || len(n.Rhs) != 1 {
				return
			}
			call, ok := n.Rhs[0].(*ast.CallExpr)
			if !ok || !isFunc(pass, call.Fun, errgroupPath, "WithContext") || len(call.Args) != 1 {
				return
			}
			parent, ok := call.Args[0].(*ast.Ident)
			if !ok {
				return
			}
			if g, ok := n.Lhs[0].(*ast.Ident); ok {
				if obj := pass.TypesInfo.ObjectOf(g); obj != nil {
					parentOf[obj] = pass.TypesInfo.ObjectOf(parent)
				}
			}
		case *ast.CallExpr:
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if ok && sel.Sel.Name == "Go" {
				if g, ok := sel.X.(*ast.Ident); ok {
					if parent := parentOf[pass.TypesInfo.ObjectOf(g)]; parent != nil {
						for _, a := range n.Args {
							if lit, ok := a.(*ast.FuncLit); ok {
								reportUses(pass, lit.Body, func(obj types.Object) bool { return obj == parent },
									"this goroutine uses %s, which isn't cancelled when the group fails: use the context errgroup.WithContext returned")
							}
						}
					}
				}
			}
			if isPkgCall(pass, n.Fun, scopePath) || isPkgCall(pass, n.Fun, schedulePath) {
				for _, a := range n.Args {
					lit, ok := a.(*ast.FuncLit)
					if !ok {
						continue
					}
					own := ownContext(pass, lit)
					if own == nil {
						continue
					}
					reportUses(pass, lit.Body, func(obj types.Object) bool {
						v, ok := obj.(*types.Var)
						return ok && v != own && isContext(v.Type()) && !within(v, lit)
					}, "this task uses the outer %s: use its own ctx parameter, which is cancelled with the task")
				}
			}
		}
	})
	return nil, nil
}

func isFunc(pass *analysis.Pass, fun ast.Expr, pkg, name string) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	f, ok := pass.TypesInfo.ObjectOf(sel.Sel).(*types.Func)
	return ok && f.Pkg() != nil && f.Pkg().Path() == pkg && f.Name() == name
}

func isPkgCall(pass *analysis.Pass, fun ast.Expr, pkg string) bool {
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
	obj := pass.TypesInfo.ObjectOf(sel.Sel)
	return obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == pkg
}

// ownContext returns a task literal's context parameter.
func ownContext(pass *analysis.Pass, lit *ast.FuncLit) *types.Var {
	if len(lit.Type.Params.List) == 0 {
		return nil
	}
	f := lit.Type.Params.List[0]
	if len(f.Names) == 0 || f.Names[0].Name == "_" || !isContext(pass.TypesInfo.TypeOf(f.Type)) {
		return nil
	}
	v, _ := pass.TypesInfo.Defs[f.Names[0]].(*types.Var)
	return v
}

func within(v *types.Var, lit *ast.FuncLit) bool { return v.Pos() >= lit.Pos() && v.Pos() < lit.End() }

var isContext = typeutil.IsContext

func reportUses(pass *analysis.Pass, body ast.Node, match func(types.Object) bool, format string) {
	ast.Inspect(body, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		if obj := pass.TypesInfo.Uses[id]; obj != nil && match(obj) {
			pass.Reportf(id.Pos(), format, id.Name)
		}
		return true
	})
}
