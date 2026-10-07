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
	case "effect":
		if p.exprLev >= 0 && p.peek() == token.LPAREN && p.isEffectLit() {
			return p.parseEffectLit()
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
		x.Label = p.interpolate(&ast.BasicLit{ValuePos: p.pos, ValueEnd: p.end(), Kind: token.STRING, Value: p.lit})
		p.next()
	}
	if p.tok == token.IDENT && p.lit == "as" {
		x.As = p.pos
		p.next()
		x.Case = p.parseTypeName(nil)
		if p.tok == token.LBRACE {
			// as Case{Field: value}: the case, with fields of its own.
			x.Case = p.parseLiteralValue(x.Case)
		}
	}
	return x
}

// isEffectLit reports whether "effect (" at the current position starts a
// function literal, "effect (params) results { body }", rather than a call
// of a function named effect: after the parameters, only a result type may
// come before "{".
func (p *parser) isEffectLit() bool {
	s := p.scanner.Lookahead()
	depth := 0
	params := true
	for {
		_, tok, _ := s.ScanToken()
		switch tok {
		case token.LPAREN, token.LBRACK:
			depth++
		case token.RPAREN, token.RBRACK:
			depth--
			if depth == 0 && tok == token.RPAREN {
				params = false
			}
		case token.LBRACE:
			return depth == 0 && !params
		case token.STRUCT, token.INTERFACE:
			// A type's body, as in chan struct{}: skip its braces.
			if !skipBraces(s) {
				return false
			}
		case token.EOF:
			return false
		case token.IDENT, token.PERIOD, token.MUL, token.COMMA, token.MAP, token.CHAN, token.ARROW, token.FUNC, token.INT, token.ELLIPSIS:
		default:
			if depth == 0 {
				return false
			}
		}
		if depth < 0 {
			return false
		}
	}
}

// skipBraces skips "{ … }", reporting whether it found the closing brace.
func skipBraces(s *scanner.Scanner) bool {
	if _, tok, _ := s.ScanToken(); tok != token.LBRACE {
		return false
	}
	for depth := 1; depth > 0; {
		switch _, tok, _ := s.ScanToken(); tok {
		case token.LBRACE:
			depth++
		case token.RBRACE:
			depth--
		case token.EOF:
			return false
		}
	}
	return true
}

// parseEffectLit parses "effect (params) results { body }".
func (p *parser) parseEffectLit() ast.Expr {
	typ := &ast.FuncType{Effect: p.pos}
	p.next()
	typ.Params = p.parseParameters(false)
	typ.Results = p.parseParameters(true)
	p.exprLev++
	body := p.parseBody()
	p.exprLev--
	return &ast.FuncLit{Type: typ, Body: body}
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
	lbrace, arms, rbrace = p.parseArms(expr, "match")
	return
}

// parseArms parses "{ arms }" of a match, or of an else. In an expression,
// each arm's body is an expression; in a statement, a block or a simple
// statement.
func (p *parser) parseArms(expr bool, context string) (lbrace token.Pos, arms []*ast.MatchArm, rbrace token.Pos) {
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
	rbrace = p.expectClosing(token.RBRACE, context)
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
			if lit, ok := s.X.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				s.X = p.interpolate(lit) // fail messages interpolate without f
			}
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
		// Read the comment before parseIdent moves past it: Go doesn't
		// order a field read against a call in the same literal.
		doc := p.leadComment
		c := &ast.SumCase{Doc: doc, Name: p.parseIdent()}
		if p.tok == token.LBRACE {
			c.Fields = &ast.FieldList{Opening: p.pos}
			p.next()
			for p.tok == token.IDENT || p.tok == token.MUL || p.tok == token.LPAREN {
				c.Fields.List = append(c.Fields.List, p.parseFieldDecl())
			}
			c.Fields.Closing = p.expect(token.RBRACE)
		}
		if p.tok == token.STRING {
			c.Message = p.interpolate(&ast.BasicLit{ValuePos: p.pos, ValueEnd: p.end(), Kind: token.STRING, Value: p.lit})
			p.next()
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

// interpolate splits a string literal into text and {expr} parts. The
// expressions are parsed in place, so their positions are real.
func (p *parser) interpolate(lit *ast.BasicLit) *ast.FString {
	fs := &ast.FString{Lit: lit}
	v := lit.Value
	base := p.file.Offset(lit.ValuePos)
	start := 1
	if v[0] == 'f' {
		start = 2
	}
	end := len(v) - 1
	if end < start {
		return fs
	}
	raw := v[end] == '`'
	var text []byte
	flush := func() {
		if len(text) > 0 {
			fs.Parts = append(fs.Parts, &ast.FStringPart{Text: string(text)})
			text = nil
		}
	}
	for j := start; j < end; j++ {
		c := v[j]
		switch {
		case c == '{' && j+1 < end && v[j+1] == '{':
			text = append(text, '{')
			j++
		case c == '}' && j+1 < end && v[j+1] == '}':
			text = append(text, '}')
			j++
		case c == '{':
			k, colon := matchBrace(v, j+1, end)
			if k < 0 {
				p.error(token.Pos(int(lit.ValuePos)+j), "unterminated { in string: write {{ for a literal brace")
				return fs
			}
			flush()
			xend, spec := k, ""
			if colon >= 0 {
				xend, spec = colon, v[colon+1:k]
			}
			fs.Parts = append(fs.Parts, &ast.FStringPart{X: p.subExpr(base+j+1, base+xend), Spec: spec})
			j = k
		case c == '}':
			p.error(token.Pos(int(lit.ValuePos)+j), "unmatched } in string: write }} for a literal brace")
		case c == '\\' && !raw && j+1 < end:
			text = append(text, v[j], v[j+1])
			j++
		default:
			text = append(text, c)
		}
	}
	flush()
	return fs
}

// matchBrace returns the index of the "}" closing an interpolation that
// starts at i, and of the first ':' at its top level (or -1).
func matchBrace(v string, i, end int) (int, int) {
	depth, colon := 0, -1
	for ; i < end; i++ {
		switch c := v[i]; c {
		case '"', '\'', '`':
			// Skip a literal inside the interpolation.
			for i++; i < end && v[i] != c; i++ {
				if v[i] == '\\' && c != '`' {
					i++
				}
			}
		case '(', '[', '{':
			depth++
		case ')', ']':
			depth--
		case '}':
			if depth == 0 {
				return i, colon
			}
			depth--
		case ':':
			if depth == 0 && colon < 0 {
				colon = i
			}
		}
	}
	return -1, -1
}

// subExpr parses the expression at file offsets [start, end).
func (p *parser) subExpr(start, end int) ast.Expr {
	q := &parser{file: p.file, mode: p.mode &^ ParseComments}
	eh := func(pos token.Position, msg string) { p.errors.Add(pos, msg) }
	q.scanner.InitRange(p.file, p.scanner.Source(), start, end, eh, 0)
	q.next()
	if q.tok == token.EOF {
		p.error(p.file.Pos(start), "empty {} in string")
		return &ast.BadExpr{From: p.file.Pos(start), To: p.file.Pos(end)}
	}
	x := q.parseRhs()
	if q.tok == token.SEMICOLON && q.lit == "\n" {
		q.next()
	}
	if q.tok != token.EOF {
		p.error(q.pos, "unexpected "+scanner.TokenString(q.tok)+" in {…}")
	}
	p.errors = append(p.errors, q.errors...)
	return x
}
