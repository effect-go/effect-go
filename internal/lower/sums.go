package lower

import (
	"strings"
	"unicode"

	"github.com/effect-go/effect-go/internal/syntax/ast"
)

// sumDecl lowers an error set or an enum.
//
// An error set, or an enum whose cases carry data, becomes a sealed
// interface (an unexported is<Name> marker method) plus one struct per case.
// An enum whose cases carry no data becomes Go's usual iota constants.
func (g *fileGen) sumDecl(d *ast.SumDecl) {
	g.w.anchor(g.off(d.Pos()))
	name := d.Name.Name
	if d.Enum && !hasData(d) {
		g.constEnum(d)
		return
	}
	var cases []string
	for _, c := range d.Cases {
		cases = append(cases, c.Name.Name)
	}
	if d.Doc == nil {
		kind := "the error set"
		if d.Enum {
			kind = "the enum"
		}
		g.w.str("// " + name + " is " + kind + " " + strings.Join(cases, " | ") + ".\n")
	}
	g.w.str("type " + name + " interface {\n")
	if !d.Enum {
		g.w.str("error\n")
	}
	g.w.str("is" + name + "()\n}\n")
	for _, c := range d.Cases {
		g.w.str("\n")
		if c.Doc != nil {
			g.copy(g.off(c.Doc.Pos()), g.off(c.Doc.End()))
			g.w.str("\n")
		}
		g.w.str("type " + c.Name.Name + " struct")
		if c.Fields != nil {
			g.copy(g.off(c.Fields.Opening), g.off(c.Fields.Closing)+1)
		} else {
			g.w.str("{}")
		}
		g.w.str("\n\nfunc (" + c.Name.Name + ") is" + name + "() {}\n")
		if d.Enum {
			continue
		}
		g.w.str("\nfunc (e " + c.Name.Name + ") Error() string { return " + g.message(c) + " }\n")
		if cause := causeField(c); cause != "" {
			g.w.str("\nfunc (e " + c.Name.Name + ") Unwrap() error { return e." + cause + " }\n")
		}
	}
	g.trimNewline()
}

func hasData(d *ast.SumDecl) bool {
	for _, c := range d.Cases {
		if c.Fields != nil {
			return true
		}
	}
	return false
}

// causeField returns the name of a case's error field, the error it wraps.
func causeField(c *ast.SumCase) string {
	if c.Fields == nil {
		return ""
	}
	for _, f := range c.Fields.List {
		if isErrorIdent(f.Type) && len(f.Names) > 0 {
			return f.Names[0].Name
		}
	}
	return ""
}

// message returns the expression for a case's Error() result: its own
// message, or the case name in words, then its fields, then its cause.
func (g *fileGen) message(c *ast.SumCase) string {
	fields := map[string]bool{}
	var names []string
	cause := causeField(c)
	if c.Fields != nil {
		for _, f := range c.Fields.List {
			for _, n := range f.Names {
				fields[n.Name] = true
				if n.Name != cause {
					names = append(names, n.Name)
				}
			}
		}
	}
	fmtName := g.pkgRef("fmt")
	if c.Message != nil {
		var format strings.Builder
		var args []string
		for _, p := range c.Message.Parts {
			if p.X == nil {
				format.WriteString(strings.ReplaceAll(p.Text, "%", "%%"))
				continue
			}
			if p.Spec != "" {
				format.WriteString("%" + p.Spec)
			} else {
				format.WriteString("%v")
			}
			args = append(args, g.fieldExpr(p.X, fields))
		}
		if len(args) == 0 {
			return `"` + format.String() + `"`
		}
		return fmtName + `.Sprintf("` + format.String() + `", ` + strings.Join(args, ", ") + ")"
	}
	text := words(c.Name.Name)
	var args []string
	if len(names) > 0 {
		var parts []string
		for _, n := range names {
			parts = append(parts, n+" %v")
			args = append(args, "e."+n)
		}
		text += " (" + strings.Join(parts, ", ") + ")"
	}
	if cause != "" {
		text += ": %v"
		args = append(args, "e."+cause)
	}
	if len(args) == 0 {
		return `"` + text + `"`
	}
	return fmtName + `.Sprintf("` + text + `", ` + strings.Join(args, ", ") + ")"
}

// fieldExpr renders an expression in a case message, reading the case's
// fields from e.
func (g *fileGen) fieldExpr(x ast.Expr, fields map[string]bool) string {
	switch x := x.(type) {
	case *ast.Ident:
		if fields[x.Name] {
			return "e." + x.Name
		}
		return x.Name
	case *ast.SelectorExpr:
		return g.fieldExpr(x.X, fields) + "." + x.Sel.Name
	case *ast.CallExpr:
		var args []string
		for _, a := range x.Args {
			args = append(args, g.fieldExpr(a, fields))
		}
		return g.fieldExpr(x.Fun, fields) + "(" + strings.Join(args, ", ") + ")"
	}
	return g.text(x)
}

// words turns NotFound into "not found".
func words(name string) string {
	var b strings.Builder
	rs := []rune(name)
	for i, r := range rs {
		if unicode.IsUpper(r) && i > 0 && (unicode.IsLower(rs[i-1]) || i+1 < len(rs) && unicode.IsLower(rs[i+1])) {
			b.WriteByte(' ')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// constEnum lowers an enum without data to iota constants and a String
// method.
func (g *fileGen) constEnum(d *ast.SumDecl) {
	name := d.Name.Name
	var cases []string
	for _, c := range d.Cases {
		cases = append(cases, c.Name.Name)
	}
	if d.Doc == nil {
		g.w.str("// " + name + " is the enum " + strings.Join(cases, " | ") + ".\n")
	}
	g.w.str("type " + name + " int\n\nconst (\n")
	for i, c := range d.Cases {
		if c.Doc != nil {
			g.copy(g.off(c.Doc.Pos()), g.off(c.Doc.End()))
			g.w.str("\n")
		}
		g.w.str(c.Name.Name)
		if i == 0 {
			g.w.str(" " + name + " = iota")
		}
		g.w.str("\n")
	}
	g.w.str(")\n\nfunc (v " + name + ") String() string {\nswitch v {\n")
	for _, c := range cases {
		g.w.str("case " + c + ":\nreturn \"" + c + "\"\n")
	}
	g.w.str("}\nreturn \"" + name + "(\" + " + g.pkgRef("strconv") + ".Itoa(int(v)) + \")\"\n}")
}
