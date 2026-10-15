package ast_test

import (
	"go/token"
	"testing"

	"github.com/effect-go/effect-go/internal/syntax/ast"
	"github.com/effect-go/effect-go/internal/syntax/parser"
)

// Inspect visits the comments of error set cases and match arms, as it
// does those of a struct's fields: here, every comment of the file.
func TestInspectComments(t *testing.T) {
	src := "package p\n\nerror E {\n\t// A doc.\n\tA // a\n}\n\nvar x = match c {\n\tA(_) => 1 // one\n}\n"
	f, err := parser.ParseFile(token.NewFileSet(), "x.ego", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[*ast.CommentGroup]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if g, ok := n.(*ast.CommentGroup); ok {
			seen[g] = true
		}
		return true
	})
	for _, g := range f.Comments {
		if !seen[g] {
			t.Errorf("not visited: %q", g.Text())
		}
	}
}
