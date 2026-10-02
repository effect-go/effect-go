// Nodes added by effect-go. Everything else in this package is a copy of
// go/ast.

package ast

import "go/token"

type (
	// A CheckExpr is "check X", "check X Label", "check X as Case" or
	// "must X". It may only start a statement or the right side of an
	// assignment; the lowering reports other uses.
	CheckExpr struct {
		Check token.Pos // position of "check" or "must"
		Must  bool      // "must" instead of "check"
		X     Expr      // the call
		Label *FString  // custom label; or nil
		As    token.Pos // position of "as"; or token.NoPos
		Case  Expr      // error set case after "as"; or nil
	}

	// An ElseExpr is "X else Fallback": on failure, use Fallback.
	ElseExpr struct {
		X        Expr
		Else     token.Pos
		Fallback Expr
	}

	// An IfExpr is "if Cond { Then } else { Else }" used as a value. Else
	// is another *IfExpr for "else if".
	IfExpr struct {
		If     token.Pos
		Cond   Expr
		Lbrace token.Pos // of Then
		Then   Expr
		Rbrace token.Pos // of Then
		Else   Expr      // *IfExpr, or the expression inside "else { … }"
		ElseL  token.Pos // "{" of the else branch; token.NoPos for "else if"
		ElseR  token.Pos // "}" of the else branch
	}

	// A LambdaExpr is "x => body" or "(a, b T) => body". Params without
	// types have Type == nil. Body is an Expr or a *BlockStmt.
	LambdaExpr struct {
		Params *FieldList // Opening is token.NoPos for a bare "x =>"
		Arrow  token.Pos
		Body   Node
	}

	// A CoalesceExpr is "X ?? Y".
	CoalesceExpr struct {
		X     Expr
		OpPos token.Pos
		Y     Expr
	}

	// An OptSelectorExpr is "X?.Sel".
	OptSelectorExpr struct {
		X    Expr
		QDot token.Pos
		Sel  *Ident
	}

	// A MatchExpr is "match Tag { arms }" used as a value.
	MatchExpr struct {
		Match  token.Pos
		Tag    Expr
		Lbrace token.Pos
		Arms   []*MatchArm
		Rbrace token.Pos
	}
)

// A MatchArm is "Patterns => Body". A pattern is nil, _, a constant, or
// Case(x) / Case(_) to match an error set or enum case and bind it. Body is
// an Expr (in a MatchExpr, or a call in a MatchStmt), a *BlockStmt or a Stmt.
type MatchArm struct {
	Patterns []Expr
	Arrow    token.Pos
	Body     Node
	Comment  *CommentGroup // line comment; or nil
}

func (a *MatchArm) Pos() token.Pos { return a.Patterns[0].Pos() }
func (a *MatchArm) End() token.Pos { return a.Body.End() }

type (
	// A MatchStmt is "match Tag { arms }" used as a statement.
	MatchStmt struct {
		Match  token.Pos
		Tag    Expr
		Lbrace token.Pos
		Arms   []*MatchArm
		Rbrace token.Pos
	}

	// A FailStmt is "fail X": return X as the function's error.
	FailStmt struct {
		Fail token.Pos
		X    Expr
	}
)

// A SumDecl is an error set ("error Name { … }") or an enum
// ("enum Name { … }").
type SumDecl struct {
	Doc     *CommentGroup
	Keyword token.Pos
	Enum    bool // "enum" rather than "error"
	Name    *Ident
	Lbrace  token.Pos
	Cases   []*SumCase
	Rbrace  token.Pos
}

// A SumCase is "Name", "Name{}" or "Name{ fields }".
type SumCase struct {
	Doc     *CommentGroup
	Name    *Ident
	Fields  *FieldList // nil without braces
	Comment *CommentGroup
}

func (c *SumCase) Pos() token.Pos { return c.Name.Pos() }
func (c *SumCase) End() token.Pos {
	if c.Fields != nil {
		return c.Fields.End()
	}
	return c.Name.End()
}

func (x *CheckExpr) Pos() token.Pos { return x.Check }
func (x *CheckExpr) End() token.Pos {
	switch {
	case x.Case != nil:
		return x.Case.End()
	case x.Label != nil:
		return x.Label.End()
	}
	return x.X.End()
}
func (x *ElseExpr) Pos() token.Pos { return x.X.Pos() }
func (x *ElseExpr) End() token.Pos { return x.Fallback.End() }
func (x *IfExpr) Pos() token.Pos   { return x.If }
func (x *IfExpr) End() token.Pos {
	if x.ElseL.IsValid() {
		return x.ElseR + 1
	}
	return x.Else.End()
}
func (x *LambdaExpr) Pos() token.Pos {
	if x.Params.Opening.IsValid() {
		return x.Params.Opening
	}
	return x.Params.Pos()
}
func (x *LambdaExpr) End() token.Pos      { return x.Body.End() }
func (x *CoalesceExpr) Pos() token.Pos    { return x.X.Pos() }
func (x *CoalesceExpr) End() token.Pos    { return x.Y.End() }
func (x *OptSelectorExpr) Pos() token.Pos { return x.X.Pos() }
func (x *OptSelectorExpr) End() token.Pos { return x.Sel.End() }
func (x *MatchExpr) Pos() token.Pos       { return x.Match }
func (x *MatchExpr) End() token.Pos       { return x.Rbrace + 1 }
func (s *MatchStmt) Pos() token.Pos       { return s.Match }
func (s *MatchStmt) End() token.Pos       { return s.Rbrace + 1 }
func (s *FailStmt) Pos() token.Pos        { return s.Fail }
func (s *FailStmt) End() token.Pos        { return s.X.End() }
func (d *SumDecl) Pos() token.Pos         { return d.Keyword }
func (d *SumDecl) End() token.Pos         { return d.Rbrace + 1 }

func (*CheckExpr) exprNode()       {}
func (*ElseExpr) exprNode()        {}
func (*IfExpr) exprNode()          {}
func (*LambdaExpr) exprNode()      {}
func (*CoalesceExpr) exprNode()    {}
func (*OptSelectorExpr) exprNode() {}
func (*MatchExpr) exprNode()       {}
func (*MatchStmt) stmtNode()       {}
func (*FailStmt) stmtNode()        {}
func (*SumDecl) declNode()         {}

// walkEgo walks the children of the effect-go nodes. It reports false for
// other nodes.
func walkEgo(v Visitor, n Node) bool {
	switch n := n.(type) {
	case *CheckExpr:
		Walk(v, n.X)
		if n.Label != nil {
			Walk(v, n.Label)
		}
		if n.Case != nil {
			Walk(v, n.Case)
		}
	case *ElseExpr:
		Walk(v, n.X)
		Walk(v, n.Fallback)
	case *IfExpr:
		Walk(v, n.Cond)
		Walk(v, n.Then)
		Walk(v, n.Else)
	case *LambdaExpr:
		Walk(v, n.Params)
		Walk(v, n.Body)
	case *CoalesceExpr:
		Walk(v, n.X)
		Walk(v, n.Y)
	case *OptSelectorExpr:
		Walk(v, n.X)
		Walk(v, n.Sel)
	case *MatchExpr:
		Walk(v, n.Tag)
		walkArms(v, n.Arms)
	case *MatchStmt:
		Walk(v, n.Tag)
		walkArms(v, n.Arms)
	case *MatchArm:
		for _, p := range n.Patterns {
			Walk(v, p)
		}
		Walk(v, n.Body)
	case *FailStmt:
		Walk(v, n.X)
	case *FString:
		for _, p := range n.Parts {
			if p.X != nil {
				Walk(v, p.X)
			}
		}
	case *SumDecl:
		if n.Doc != nil {
			Walk(v, n.Doc)
		}
		Walk(v, n.Name)
		for _, c := range n.Cases {
			Walk(v, c)
		}
	case *SumCase:
		if n.Doc != nil {
			Walk(v, n.Doc)
		}
		Walk(v, n.Name)
		if n.Fields != nil {
			Walk(v, n.Fields)
		}
	default:
		return false
	}
	return true
}

func walkArms(v Visitor, arms []*MatchArm) {
	for _, a := range arms {
		Walk(v, a)
	}
}

// An FString is an interpolated string: an f"…" literal, a check label or a
// fail message.
type FString struct {
	Lit   *BasicLit
	Parts []*FStringPart
}

// An FStringPart is either text or an interpolated expression.
type FStringPart struct {
	Text string // source text, escapes kept; "{{" and "}}" already folded
	X    Expr   // interpolated expression; nil for text
	Spec string // format spec after ':' in {x:spec}; or ""
}

func (x *FString) Pos() token.Pos { return x.Lit.Pos() }
func (x *FString) End() token.Pos { return x.Lit.End() }
func (*FString) exprNode()        {}

// Raw reports whether the literal is backquoted.
func (x *FString) Raw() bool {
	v := x.Lit.Value
	return len(v) > 0 && v[len(v)-1] == '`'
}
