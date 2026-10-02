// Parsing of the effect-go additions. Everything else in this package is a
// copy of go/parser, with small hooks into the functions below.

package parser

import (
	"go/token"

	"github.com/effect-go/effect-go/internal/syntax/ast"
	"github.com/effect-go/effect-go/internal/syntax/scanner"
)

// peek returns the token after the current one, skipping comments.
func (p *parser) peek() token.Token {
	_, tok, _ := p.scanner.Lookahead().ScanToken()
	return tok
}

// startsOperand reports whether tok can follow a contextual keyword such as
// check, fail or match. It's what makes "check x" new syntax while
// "check(x)", "check := 1" and "check.x" stay ordinary Go.
func startsOperand(tok token.Token) bool {
	switch tok {
	case token.IDENT, token.STRING, token.INT, token.FLOAT, token.CHAR, token.AND:
		return true
	}
	return false
}

// parseEgoOperand parses an effect-go expression starting at an identifier,
// or returns nil.
func (p *parser) parseEgoOperand() ast.Expr {
	switch p.lit {
	case "check", "must":
		if p.peek() == token.IDENT {
			return p.parseCheck()
		}
	case "match":
		if p.peek() == token.IDENT {
			arms, m, tag, l, r := p.parseMatch(true)
			return &ast.MatchExpr{Match: m, Tag: tag, Lbrace: l, Arms: arms, Rbrace: r}
		}
	}
	if !p.inPattern && p.peek() == scanner.FATARROW {
		name := p.parseIdent()
		params := &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{name}}}}
		return p.parseLambdaBody(params)
	}
	return nil
}

func (p *parser) parseCheck() ast.Expr {
	x := &ast.CheckExpr{Check: p.pos, Must: p.lit == "must"}
	p.next()
	x.X = p.parsePrimaryExpr(nil)
	if x.Must {
		return x
	}
	if p.tok == token.STRING {
		x.Label = &ast.BasicLit{ValuePos: p.pos, ValueEnd: p.end(), Kind: token.STRING, Value: p.lit}
		p.next()
	}
	if p.tok == token.IDENT && p.lit == "as" {
		x.As = p.pos
		p.next()
		x.Case = p.parseTypeName(nil)
	}
	return x
}

// isLambdaParams reports whether the "(" at the current position opens the
// parameters of a lambda: its matching ")" is followed by "=>".
func (p *parser) isLambdaParams() bool {
	s := p.scanner.Lookahead()
	depth := 1
	for {
		_, tok, _ := s.ScanToken()
		switch tok {
		case token.LPAREN, token.LBRACK, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACK, token.RBRACE:
			depth--
			if depth == 0 {
				_, next, _ := s.ScanToken()
				return tok == token.RPAREN && next == scanner.FATARROW
			}
		case token.EOF:
			return false
		}
	}
}

func (p *parser) parseLambda() ast.Expr {
	params := p.parseParameters(false)
	named := false
	for _, f := range params.List {
		if len(f.Names) > 0 {
			named = true
		}
	}
	if !named {
		// (a, b) => …: the parameter list parsed the names as types.
		for _, f := range params.List {
			id, ok := f.Type.(*ast.Ident)
			if !ok {
				p.error(f.Type.Pos(), "expected parameter name")
				id = &ast.Ident{NamePos: f.Type.Pos(), Name: "_"}
			}
			f.Names, f.Type = []*ast.Ident{id}, nil
		}
	}
	return p.parseLambdaBody(params)
}

func (p *parser) parseLambdaBody(params *ast.FieldList) ast.Expr {
	arrow := p.pos
	p.next() // =>
	x := &ast.LambdaExpr{Params: params, Arrow: arrow}
	if p.tok == token.LBRACE {
		p.exprLev++
		x.Body = p.parseBody()
		p.exprLev--
	} else {
		x.Body = p.parseRhs()
	}
	return x
}

// parseIfExpr parses "if cond { x } else { y }" in an expression.
func (p *parser) parseIfExpr() ast.Expr {
	x := &ast.IfExpr{If: p.expect(token.IF)}
	old := p.exprLev
	p.exprLev = -1
	x.Cond = p.parseRhs()
	p.exprLev = old
	x.Lbrace, x.Then, x.Rbrace = p.parseBracedExpr()
	if p.tok != token.ELSE {
		p.error(p.pos, "an if expression needs an else branch")
		x.Else = &ast.BadExpr{From: p.pos, To: p.pos}
		return x
	}
	p.next()
	if p.tok == token.IF {
		x.Else = p.parseIfExpr()
	} else {
		x.ElseL, x.Else, x.ElseR = p.parseBracedExpr()
	}
	return x
}

func (p *parser) parseBracedExpr() (token.Pos, ast.Expr, token.Pos) {
	l := p.expect(token.LBRACE)
	p.exprLev++
	x := p.parseRhs()
	p.exprLev--
	if p.tok == token.SEMICOLON && p.lit == "\n" {
		p.next()
	}
	r := p.expect(token.RBRACE)
	return l, x, r
}

// parseMatch parses "match tag { arms }". In an expression, each arm's body
// is an expression; in a statement, a block or a simple statement.
func (p *parser) parseMatch(expr bool) (arms []*ast.MatchArm, match token.Pos, tag ast.Expr, lbrace, rbrace token.Pos) {
	match = p.pos
	p.next()
	old := p.exprLev
	p.exprLev = -1
	tag = p.parseRhs()
	p.exprLev = old
	lbrace = p.expect(token.LBRACE)
	p.exprLev++
	for p.tok != token.RBRACE && p.tok != token.EOF {
		arm := &ast.MatchArm{}
		p.inPattern = true
		arm.Patterns = p.parseExprList()
		p.inPattern = false
		arm.Arrow = p.expect(scanner.FATARROW)
		switch {
		case p.tok == token.LBRACE:
			b := p.parseBlockStmt()
			arm.Body = b
			arm.Comment = p.expectSemi()
		case expr:
			arm.Body = p.parseRhs()
			arm.Comment = p.expectSemi()
		default:
			arm.Body = p.parseStmt()
		}
		arms = append(arms, arm)
	}
	p.exprLev--
	rbrace = p.expectClosing(token.RBRACE, "match")
	return
}

// parseEgoStmt parses an effect-go statement starting at an identifier, or
// returns nil.
func (p *parser) parseEgoStmt() ast.Stmt {
	switch p.lit {
	case "fail":
		if startsOperand(p.peek()) {
			s := &ast.FailStmt{Fail: p.pos}
			p.next()
			s.X = p.parseRhs()
			p.expectSemi()
			return s
		}
	case "match":
		if p.peek() == token.IDENT {
			arms, m, tag, l, r := p.parseMatch(false)
			p.expectSemi()
			return &ast.MatchStmt{Match: m, Tag: tag, Lbrace: l, Arms: arms, Rbrace: r}
		}
	}
	return nil
}

// parseEgoDecl parses a top-level effect-go declaration ("effect …",
// "error Name {", "enum Name {"), or returns nil.
func (p *parser) parseEgoDecl() ast.Decl {
	switch p.lit {
	case "effect":
		switch p.peek() {
		case token.IDENT, token.LPAREN:
			return p.parseFuncDecl()
		case token.FUNC:
			p.error(p.pos, "write effect Name(…) without func")
			p.next()
			return p.parseFuncDecl()
		}
	case "error", "enum":
		if p.peek() == token.IDENT {
			return p.parseSumDecl()
		}
	}
	return nil
}

func (p *parser) parseSumDecl() *ast.SumDecl {
	d := &ast.SumDecl{Doc: p.leadComment, Keyword: p.pos, Enum: p.lit == "enum"}
	p.next()
	d.Name = p.parseIdent()
	d.Lbrace = p.expect(token.LBRACE)
	for p.tok == token.IDENT {
		c := &ast.SumCase{Doc: p.leadComment, Name: p.parseIdent()}
		if p.tok == token.LBRACE {
			c.Fields = &ast.FieldList{Opening: p.pos}
			p.next()
			for p.tok == token.IDENT || p.tok == token.MUL || p.tok == token.LPAREN {
				c.Fields.List = append(c.Fields.List, p.parseFieldDecl())
			}
			c.Fields.Closing = p.expect(token.RBRACE)
		}
		if p.tok == token.RBRACE {
			c.Comment = p.lineComment
		} else {
			c.Comment = p.expectSemi()
		}
		d.Cases = append(d.Cases, c)
	}
	d.Rbrace = p.expect(token.RBRACE)
	p.expectSemi()
	return d
}
