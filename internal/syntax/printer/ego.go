// Printing of the effect-go additions. Everything else in this package is a
// copy of go/printer, with small hooks into the functions below.

package printer

import (
	"go/token"

	"github.com/effect-go/effect-go/internal/syntax/ast"
	"github.com/effect-go/effect-go/internal/syntax/scanner"
)

// keyword prints a contextual keyword such as check or match. It prints
// like an identifier, which is what the scanner sees.
func (p *printer) keyword(pos token.Pos, name string) {
	p.setPos(pos)
	p.print(&ast.Ident{NamePos: pos, Name: name})
}

// egoExpr prints an effect-go expression. It reports false for other nodes.
func (p *printer) egoExpr(expr ast.Expr, prec1, depth int) bool {
	switch x := expr.(type) {
	case *ast.CheckExpr:
		if x.Must {
			p.keyword(x.Check, "must")
		} else {
			p.keyword(x.Check, "check")
		}
		p.print(blank)
		p.expr1(x.X, token.HighestPrec, depth)
		if x.Label != nil {
			p.print(blank)
			p.expr(x.Label)
		}
		if x.Case != nil {
			p.print(blank)
			p.keyword(x.As, "as")
			p.print(blank)
			p.expr(x.Case)
		}

	case *ast.ElseExpr:
		p.expr1(x.X, token.LowestPrec+1, depth)
		p.print(blank)
		p.setPos(x.Else)
		p.print(token.ELSE, blank)
		p.expr1(x.Fallback, token.LowestPrec+1, depth)

	case *ast.CoalesceExpr:
		paren := prec1 > token.LowestPrec
		if paren {
			p.print(token.LPAREN)
		}
		p.expr1(x.X, token.LowestPrec+1, depth)
		p.print(blank)
		p.setPos(x.OpPos)
		p.print(scanner.QQ, blank)
		p.expr1(x.Y, token.LowestPrec, depth)
		if paren {
			p.print(token.RPAREN)
		}

	case *ast.OptSelectorExpr:
		p.expr1(x.X, token.HighestPrec, depth)
		p.setPos(x.QDot)
		p.print(scanner.QDOT)
		p.expr(x.Sel)

	case *ast.IfExpr:
		p.ifExpr(x)

	case *ast.LambdaExpr:
		switch {
		case !x.Params.Opening.IsValid() && len(x.Params.List) == 1 && x.Params.List[0].Type == nil:
			p.expr(x.Params.List[0].Names[0])
		case untypedParams(x.Params):
			p.setPos(x.Params.Opening)
			p.print(token.LPAREN)
			for i, f := range x.Params.List {
				for j, n := range f.Names {
					if i > 0 || j > 0 {
						p.print(token.COMMA, blank)
					}
					p.expr(n)
				}
			}
			p.setPos(x.Params.Closing)
			p.print(token.RPAREN)
		default:
			p.parameters(x.Params, funcParam)
		}
		p.print(blank)
		p.setPos(x.Arrow)
		p.print(scanner.FATARROW)
		switch b := x.Body.(type) {
		case *ast.BlockStmt:
			p.funcBody(p.distanceFrom(x.Pos(), 0), blank, b)
		case ast.Expr:
			p.print(blank)
			p.expr(b)
		}

	case *ast.MatchExpr:
		p.match(x.Match, x.Tag, x.Lbrace, x.Arms, x.Rbrace)

	default:
		return false
	}
	return true
}

func (p *printer) ifExpr(x *ast.IfExpr) {
	p.setPos(x.If)
	p.print(token.IF, blank)
	p.expr(x.Cond)
	p.print(blank)
	p.bracedExpr(x.Lbrace, x.Then, x.Rbrace)
	p.print(blank, token.ELSE, blank)
	if e, ok := x.Else.(*ast.IfExpr); ok && !x.ElseL.IsValid() {
		p.ifExpr(e)
		return
	}
	p.bracedExpr(x.ElseL, x.Else, x.ElseR)
}

func (p *printer) bracedExpr(l token.Pos, x ast.Expr, r token.Pos) {
	p.setPos(l)
	p.print(token.LBRACE, blank)
	p.expr(x)
	p.print(blank, noExtraLinebreak)
	p.setPos(r)
	p.print(token.RBRACE, noExtraLinebreak)
}

// match prints a match statement or expression. The "=>" of consecutive
// one-line arms line up, like the values of a composite literal.
func (p *printer) match(pos token.Pos, tag ast.Expr, lbrace token.Pos, arms []*ast.MatchArm, rbrace token.Pos) {
	p.keyword(pos, "match")
	p.print(blank)
	p.expr(stripParens(tag))
	p.print(blank)
	p.setPos(lbrace)
	p.print(token.LBRACE, indent)
	var line int
	for i, a := range arms {
		p.linebreak(p.lineFor(a.Pos()), 1, ignore, i == 0 || p.linesFrom(line) > 0)
		p.recordLine(&line)
		p.exprList(token.NoPos, a.Patterns, 1, 0, a.Arrow, false)
		p.print(vtab)
		p.setPos(a.Arrow)
		p.print(scanner.FATARROW, blank)
		switch b := a.Body.(type) {
		case *ast.BlockStmt:
			p.block(b, 1)
		case ast.Expr:
			p.expr(b)
		case ast.Stmt:
			p.stmt(b, false)
		}
		if a.Comment != nil {
			p.setComment(a.Comment)
		}
	}
	p.print(unindent)
	p.linebreak(p.lineFor(rbrace), 1, ignore, true)
	p.setPos(rbrace)
	p.print(token.RBRACE)
}

// egoStmt prints an effect-go statement. It reports false for other nodes.
func (p *printer) egoStmt(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case *ast.MatchStmt:
		p.match(s.Match, s.Tag, s.Lbrace, s.Arms, s.Rbrace)
	case *ast.FailStmt:
		p.keyword(s.Fail, "fail")
		p.print(blank)
		p.expr0(s.X, 1)
	default:
		return false
	}
	return true
}

// sumDecl prints an error set or enum. One-field cases stay on one line, as
// in the source.
func (p *printer) sumDecl(d *ast.SumDecl) {
	p.setComment(d.Doc)
	if d.Enum {
		p.keyword(d.Keyword, "enum")
	} else {
		p.keyword(d.Keyword, "error")
	}
	p.print(blank)
	p.expr(d.Name)
	p.print(blank)
	p.setPos(d.Lbrace)
	p.print(token.LBRACE, indent)
	var line int
	for i, c := range d.Cases {
		p.linebreak(p.lineFor(c.Pos()), 1, ignore, i == 0 || p.linesFrom(line) > 0)
		p.setComment(c.Doc)
		p.recordLine(&line)
		p.expr(c.Name)
		if c.Fields != nil {
			p.fieldList(c.Fields, true, false)
		}
		if c.Comment != nil {
			p.print(vtab)
			p.setComment(c.Comment)
		}
	}
	p.print(unindent)
	p.linebreak(p.lineFor(d.Rbrace), 1, ignore, true)
	p.setPos(d.Rbrace)
	p.print(token.RBRACE)
}

func untypedParams(l *ast.FieldList) bool {
	for _, f := range l.List {
		if f.Type == nil {
			return true
		}
	}
	return false
}
