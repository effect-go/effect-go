package lower

import (
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	"go/token"
	"go/types"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/effect-go/effect-go/internal/syntax/ast"
)

// pkgGen holds what's shared by the files of one package.
type pkgGen struct {
	cfg     Config
	fset    *token.FileSet
	name    string // package name
	path    string // import path
	files   []*fileGen
	goFiles []string

	// Test files, compiled in their own passes.
	testFiles, xtestFiles []*fileGen // package x, package x_test
	testGo, xtestGo       []string
	override              map[string]*types.Package // imports checked here

	topNames map[string]bool         // package-level names, from all files
	sums     map[string]*ast.SumDecl // error sets and enums declared in .ego files
	setFuncs map[string]*ast.SumDecl // "Recv.Name" or "Name" of functions returning an error set
	facts    map[ast.Node]*fact      // what drafts learned, kept across rounds
}

// A fact is type information about a node that survives the draft rounds.
type fact struct {
	sig      *types.Signature // expected type of a lambda
	bodyType types.Type       // type of a typed lambda's expression body
	openRes  bool             // sig is a generic function's: its results wait for inference
}

func (p *pkgGen) fact(n ast.Node) *fact {
	f := p.facts[n]
	if f == nil {
		f = &fact{}
		p.facts[n] = f
	}
	return f
}

// collect records the package-level declarations.
func (p *pkgGen) collect() {
	p.topNames = map[string]bool{}
	p.sums = map[string]*ast.SumDecl{}
	p.setFuncs = map[string]*ast.SumDecl{}
	for _, f := range p.files {
		for _, d := range f.file.Decls {
			switch d := d.(type) {
			case *ast.SumDecl:
				p.topNames[d.Name.Name] = true
				p.sums[d.Name.Name] = d
				for _, c := range d.Cases {
					p.topNames[c.Name.Name] = true
				}
			case *ast.FuncDecl:
				if d.Recv == nil {
					p.topNames[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						p.topNames[s.Name.Name] = true
					case *ast.ValueSpec:
						for _, n := range s.Names {
							p.topNames[n.Name] = true
						}
					}
				}
			}
		}
	}
	for _, f := range p.files {
		for _, d := range f.file.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				if set := p.resultSet(fd.Type); set != nil {
					p.setFuncs[funcKey(fd)] = set
				}
			}
		}
	}
	for _, path := range p.goFiles {
		src, err := p.read(path)
		if err != nil {
			continue
		}
		gf, err := goparser.ParseFile(token.NewFileSet(), path, src, goparser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, d := range gf.Decls {
			switch d := d.(type) {
			case *goast.FuncDecl:
				if d.Recv == nil {
					p.topNames[d.Name.Name] = true
				}
			case *goast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *goast.TypeSpec:
						p.topNames[s.Name.Name] = true
					case *goast.ValueSpec:
						for _, n := range s.Names {
							p.topNames[n.Name] = true
						}
					}
				}
			}
		}
	}
}

// resultSet returns the error set a function type's last result names.
func (p *pkgGen) resultSet(ft *ast.FuncType) *ast.SumDecl {
	if ft.Results == nil || len(ft.Results.List) == 0 {
		return nil
	}
	last := ft.Results.List[len(ft.Results.List)-1]
	if id, ok := last.Type.(*ast.Ident); ok {
		if s := p.sums[id.Name]; s != nil && !s.Enum {
			return s
		}
	}
	return nil
}

func funcKey(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	return recvName(fd.Recv.List[0].Type) + "." + fd.Name.Name
}

func recvName(x ast.Expr) string {
	switch x := x.(type) {
	case *ast.StarExpr:
		return recvName(x.X)
	case *ast.IndexExpr:
		return recvName(x.X)
	case *ast.IndexListExpr:
		return recvName(x.X)
	case *ast.Ident:
		return x.Name
	}
	return ""
}

// round says what kind of rendering is under way.
type round struct {
	final bool
	ti    *typeInfo // the last draft's types; nil in the first round
}

// An edit replaces src[off:end] (an insertion when end == off) while text
// is copied verbatim.
type edit struct {
	off, end int
	text     string
}

// fileGen renders one .ego file.
type fileGen struct {
	pkg  *pkgGen
	name string // users.ego
	path string
	src  []byte
	file *ast.File
	tf   *token.File

	// Per rendering.
	r       *round
	w, pre  *writer // current output, and where hoisted statements go
	edits   []edit
	needs   map[ast.Node]bool
	lowered map[ast.Stmt]bool // statements rendered by lowerStmt
	names   map[string]string // import path -> name, from the file's imports
	added   map[string]string // import path -> name, added by lowering
	taken   map[string]bool   // names of imports
	diags   []Diagnostic
	fn      *funcState

	exprPats map[*ast.CallExpr]bool
	parents  map[ast.Node]ast.Node
}

// funcState describes the function being rendered.
type funcState struct {
	outer   *funcState
	results []ast.Expr   // result type expressions, error excluded
	types   []types.Type // their types, when known
	hasErr  bool         // the last result is an error
	set     *ast.SumDecl // the error set it returns; or nil
	effect  bool         // declared with effect
	ctx     bool         // a ctx is in scope
	used    map[string]bool
	temps   map[string]bool
}

func (g *fileGen) off(p token.Pos) int { return g.tf.Offset(p) }

func (g *fileGen) errorf(pos token.Pos, format string, args ...any) {
	if g.r.final {
		g.diags = append(g.diags, Diagnostic{Pos: g.tf.Position(pos), Msg: fmt.Sprintf(format, args...)})
	}
}

// text returns the source text of a node.
func (g *fileGen) text(n ast.Node) string { return string(g.src[g.off(n.Pos()):g.off(n.End())]) }

// render renders the file for one round.
func (g *fileGen) render(r *round) *writer {
	g.r = r
	g.diags = nil
	g.added = map[string]string{}
	g.names = map[string]string{}
	g.taken = map[string]bool{}
	for _, s := range g.file.Imports {
		p, _ := strconv.Unquote(s.Path.Value)
		name := path.Base(p)
		if s.Name != nil {
			name = s.Name.Name
		} else if g.r.ti != nil {
			for _, imp := range g.r.ti.pkg.Imports() {
				if imp.Path() == p {
					name = imp.Name()
				}
			}
		}
		g.names[p] = name
		g.taken[name] = true
	}
	g.autoImports()
	g.computeEdits()
	g.computeNeeds()

	body := &writer{}
	g.w, g.pre = body, nil
	importsEnd := g.off(g.file.Name.End())
	first := len(g.file.Decls)
	for i, d := range g.file.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			importsEnd = g.off(gd.End())
			continue
		}
		first = i
		break
	}
	cur := importsEnd
	for _, d := range g.file.Decls[first:] {
		start := d.Pos()
		if doc := declDoc(d); doc != nil {
			start = doc.Pos()
		}
		g.copy(cur, g.off(start))
		g.mark(start)
		g.copy(g.off(start), g.off(d.Pos()))
		g.node(d)
		cur = g.off(d.End())
	}
	g.copy(cur, len(g.src))

	out := &writer{}
	out.str("// Code generated by ego generate from " + g.name + ". DO NOT EDIT.\n\n")
	g.w = out
	// Imports added by lowering go into the file's import block.
	var lp, single *ast.GenDecl
	for _, d := range g.file.Decls[:first] {
		if gd := d.(*ast.GenDecl); gd.Lparen.IsValid() {
			lp = gd
		} else {
			single = gd
		}
	}
	paths := make([]string, 0, len(g.added))
	for p := range g.added {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	spec := func(p string) string {
		if g.added[p] != path.Base(p) {
			return g.added[p] + " " + strconv.Quote(p)
		}
		return strconv.Quote(p)
	}
	switch {
	case len(paths) == 0:
		g.copy(0, importsEnd)
	case lp == nil && single != nil:
		// import "x" becomes a block with the added imports.
		g.copy(0, g.off(single.Pos()))
		g.importBlock(paths, spec, single.Specs[0].(*ast.ImportSpec))
		g.copy(g.off(single.End()), importsEnd)
	case lp != nil:
		g.copy(0, g.off(lp.Lparen)+1)
		for _, p := range paths {
			if isStd(p) {
				out.str("\n\t" + spec(p))
			}
		}
		g.copy(g.off(lp.Lparen)+1, g.off(lp.Rparen))
		for i, p := range nonStd(paths) {
			if i == 0 {
				out.str("\n")
			}
			out.str("\t" + spec(p) + "\n")
		}
		g.copy(g.off(lp.Rparen), importsEnd)
	default:
		g.copy(0, importsEnd)
		out.str("\n\n")
		g.importBlock(paths, spec, nil)
	}
	out.add(body)
	return out
}

// use returns the name to refer to an imported package by, importing it if
// the file doesn't.
func (g *fileGen) use(p, name string) string {
	if n, ok := g.names[p]; ok {
		return n
	}
	if n, ok := g.added[p]; ok {
		return n
	}
	n := name
	for i := 2; g.taken[n] || g.pkg.topNames[n]; i++ {
		n = "ego" + name
		if i > 2 {
			n += strconv.Itoa(i)
		}
	}
	g.added[p] = n
	g.taken[n] = true
	return n
}

func (g *fileGen) pkgRef(p string) string { return g.use(p, path.Base(p)) }

// copy copies src[a:b], applying edits. A replacement applies when it
// starts in [a, b); an insertion belongs to the gap before its offset, so
// it applies when its offset is in (a, b].
func (g *fileGen) copy(a, b int) {
	start := a
	for _, e := range g.edits {
		if e.end > e.off {
			if e.off < start || e.off >= b {
				continue
			}
		} else if e.off <= start || e.off > b {
			continue
		}
		g.w.copySrc(g.src, a, e.off)
		g.w.anchor(e.off)
		g.w.str(e.text)
		a = max(a, e.end)
	}
	g.w.copySrc(g.src, a, b)
}

// mark records that the statement or declaration at pos starts here, for
// //line directives.
func (g *fileGen) mark(pos token.Pos) {
	if g.r.final {
		g.w.marks = append(g.w.marks, mark{len(g.w.buf), g.tf.Line(pos)})
	}
}

// node renders n: verbatim where possible, rewriting what needs it.
func (g *fileGen) node(n ast.Node) {
	if !g.needs[n] {
		g.copy(g.off(n.Pos()), g.off(n.End()))
		return
	}
	switch n := n.(type) {
	case *ast.SumDecl:
		g.sumDecl(n)
		return
	case *ast.FuncDecl:
		g.funcDecl(n)
		return
	case *ast.FuncLit:
		g.funcLit(n)
		return
	case ast.Stmt:
		if g.lowered[n] {
			g.stmt(n)
			return
		}
	case ast.Expr:
		// Record where rewritten expressions went, so drafts can type them.
		start := len(g.w.buf)
		if !g.expr(n) {
			g.children(n)
		}
		g.w.recs = append(g.w.recs, rec{n, start, len(g.w.buf)})
		return
	}
	g.children(n)
}

// children renders n's source with its children rendered by node.
func (g *fileGen) children(n ast.Node) {
	cur := g.off(n.Pos())
	_, isBlock := n.(*ast.BlockStmt)
	_, isClause := n.(*ast.CaseClause)
	for _, c := range childNodes(n) {
		g.copy(cur, g.off(c.Pos()))
		if _, ok := c.(ast.Stmt); ok && (isBlock || isClause) {
			g.mark(c.Pos())
		}
		g.node(c)
		cur = g.off(c.End())
	}
	g.copy(cur, g.off(n.End()))
}

// childNodes returns n's children that lie within it, in source order.
func childNodes(n ast.Node) []ast.Node {
	var out []ast.Node
	ast.Inspect(n, func(c ast.Node) bool {
		if c == n {
			return true
		}
		if c == nil {
			return false
		}
		switch c.(type) {
		case *ast.CommentGroup, *ast.Comment:
			return false
		}
		if c.Pos() >= n.Pos() && c.End() <= n.End() && c.Pos().IsValid() {
			out = append(out, c)
		}
		return false
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].Pos() < out[j].Pos() })
	return out
}

// computeNeeds marks the nodes that can't be copied verbatim, and their
// ancestors.
func (g *fileGen) computeNeeds() {
	g.needs = map[ast.Node]bool{}
	g.lowered = map[ast.Stmt]bool{}
	var stack []ast.Node
	markUp := func() {
		for i := len(stack) - 1; i >= 0; i-- {
			if g.needs[stack[i]] {
				break
			}
			g.needs[stack[i]] = true
		}
	}
	ast.Inspect(g.file, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		switch n := n.(type) {
		case *ast.CheckExpr, *ast.ElseExpr, *ast.IfExpr, *ast.LambdaExpr, *ast.CoalesceExpr,
			*ast.OptSelectorExpr, *ast.MatchExpr, *ast.MatchStmt, *ast.FailStmt, *ast.SumDecl, *ast.FString:
			markUp()
		case *ast.CallExpr:
			if g.builtin(n) != "" {
				markUp()
			}
		case *ast.Ident:
			if !g.r.final && g.matchBinding(n) != nil {
				markUp()
			}
		}
		return true
	})
	// Statements with dialect expressions in them (outside nested function
	// bodies) are lowered as a whole.
	ast.Inspect(g.file, func(n ast.Node) bool {
		s, ok := n.(ast.Stmt)
		if !ok || !g.needs[n] {
			return true
		}
		switch s.(type) {
		case *ast.BlockStmt, *ast.LabeledStmt, *ast.CaseClause, *ast.CommClause:
			return true
		case *ast.FailStmt, *ast.MatchStmt:
			g.lowered[s] = true
			return true
		}
		if hasDirect(s, g) {
			g.lowered[s] = true
		}
		return true
	})
}

// hasDirect reports whether s has dialect expressions in its own
// expressions, rather than in nested statements or function bodies.
func hasDirect(s ast.Stmt, g *fileGen) bool {
	found := false
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		if found || n == nil {
			return false
		}
		switch n := n.(type) {
		case *ast.BlockStmt, *ast.FuncLit, *ast.LambdaExpr:
			if _, isLambda := n.(*ast.LambdaExpr); isLambda {
				found = true // the lambda itself is rendered by the statement
			}
			return false
		case *ast.CheckExpr, *ast.ElseExpr, *ast.IfExpr, *ast.CoalesceExpr, *ast.OptSelectorExpr, *ast.MatchExpr, *ast.FString:
			found = true
			return false
		case *ast.CallExpr:
			if g.builtin(n) != "" {
				found = true
				return false
			}
		case *ast.Ident:
			if !g.r.final && g.matchBinding(n) != nil {
				found = true
			}
		}
		return true
	}
	ast.Inspect(s, func(n ast.Node) bool {
		if n == s {
			return true
		}
		return visit(n)
	})
	return found
}

// builtin returns "all", "race", "retry", "timeout" or "each" if call uses one of the
// predeclared combinators, which the package's own declarations shadow.
func (g *fileGen) builtin(call *ast.CallExpr) string {
	id, ok := call.Fun.(*ast.Ident)
	if !ok {
		return ""
	}
	switch id.Name {
	case "all", "race", "retry", "timeout", "each":
		if id.Obj == nil && !g.pkg.topNames[id.Name] {
			return id.Name
		}
	}
	return ""
}

// matchBinding returns the pattern that declares id, if id is bound by an
// arm of a match expression.
func (g *fileGen) matchBinding(id *ast.Ident) *ast.CallExpr {
	if id.Obj == nil {
		return nil
	}
	c, ok := id.Obj.Decl.(*ast.CallExpr)
	if !ok || !g.exprPatterns()[c] {
		return nil
	}
	for _, a := range c.Args {
		if a == id {
			return nil // the declaration itself
		}
	}
	return c
}

// computeEdits computes the textual edits applied while copying: effect
// headers, implicit ctx arguments, and error sets in signatures.
func (g *fileGen) computeEdits() {
	g.edits = nil
	ast.Inspect(g.file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			g.funcHeaderEdits(n.Type, n.Body, n)
			if n.Type.Effect.IsValid() && n.Body != nil {
				g.ctxEdits(n.Body)
			}
		case *ast.FuncLit:
			g.funcHeaderEdits(n.Type, n.Body, nil)
		case *ast.InterfaceType:
			for _, m := range n.Methods.List {
				ft, ok := m.Type.(*ast.FuncType)
				if !ok || !ft.Effect.IsValid() || len(m.Names) == 0 {
					continue
				}
				g.edits = append(g.edits, edit{g.off(ft.Effect), g.off(m.Names[0].Pos()), ""})
				g.ctxParamEdit(ft)
				if set := g.pkg.resultSet(ft); set != nil {
					last := ft.Results.List[len(ft.Results.List)-1]
					g.edits = append(g.edits, edit{g.off(last.Type.Pos()), g.off(last.Type.End()), "error"})
				}
			}
		case *ast.CallExpr:
			if b := g.builtin(n); b != "" {
				// The branches get their own ctx.
				args := n.Args
				if b == "retry" || b == "timeout" {
					args = args[min(1, len(args)):]
				}
				for _, a := range args {
					g.ctxEdits(a)
				}
			}
		}
		return true
	})
	sort.SliceStable(g.edits, func(i, j int) bool { return g.edits[i].off < g.edits[j].off })
	g.edits = slices.CompactFunc(g.edits, func(a, b edit) bool { return a == b })
}

func (g *fileGen) ctxParamEdit(ft *ast.FuncType) {
	text := g.pkgRef("context") + ".Context"
	if len(ft.Params.List) > 0 {
		text += ", "
	}
	g.edits = append(g.edits, edit{g.off(ft.Params.Opening) + 1, g.off(ft.Params.Opening) + 1, "ctx " + text})
	for _, f := range ft.Params.List {
		for _, n := range f.Names {
			if n.Name == "ctx" {
				g.errorf(n.Pos(), "effect functions declare ctx themselves: remove this parameter")
			}
		}
	}
}

// funcHeaderEdits rewrites an effect function's header, and an error set
// result to error.
func (g *fileGen) funcHeaderEdits(ft *ast.FuncType, body *ast.BlockStmt, decl *ast.FuncDecl) {
	set := g.pkg.resultSet(ft)
	if !ft.Effect.IsValid() {
		if set != nil {
			// The set's interface type stays declared; signatures return error.
			last := ft.Results.List[len(ft.Results.List)-1]
			g.edits = append(g.edits, edit{g.off(last.Type.Pos()), g.off(last.Type.End()), "error"})
		}
		return
	}
	g.edits = append(g.edits, edit{g.off(ft.Effect), g.off(ft.Effect) + len("effect"), "func"})
	g.ctxParamEdit(ft)
	errName := ""
	if ft.Results != nil && len(ft.Results.List) > 0 {
		// Name every result, keeping the type expressions verbatim.
		n := 0
		for _, f := range ft.Results.List {
			n += max(1, len(f.Names))
		}
		l := ft.Results
		if !l.Opening.IsValid() {
			g.edits = append(g.edits, edit{g.off(l.Pos()), g.off(l.Pos()), "("})
		}
		i := 0
		for _, f := range ft.Results.List {
			i += max(1, len(f.Names))
			last := i == n
			isErr := last && (set != nil || isErrorIdent(f.Type))
			if len(f.Names) == 0 {
				name := "_ "
				if isErr {
					name = "err "
				}
				g.edits = append(g.edits, edit{g.off(f.Type.Pos()), g.off(f.Type.Pos()), name})
			}
			if isErr {
				errName = "err"
				if len(f.Names) > 0 {
					errName = f.Names[len(f.Names)-1].Name
					if errName == "_" {
						errName = "err"
						nm := f.Names[len(f.Names)-1]
						g.edits = append(g.edits, edit{g.off(nm.Pos()), g.off(nm.End()), "err"})
					}
				}
				if set != nil {
					g.edits = append(g.edits, edit{g.off(f.Type.Pos()), g.off(f.Type.End()), "error"})
				}
			}
		}
		if !l.Opening.IsValid() {
			g.edits = append(g.edits, edit{g.off(l.End()), g.off(l.End()), ")"})
		}
	}
	if body == nil || decl == nil {
		return
	}
	spanName := g.pkg.name + "." + funcKey(decl)
	tr := g.pkgRef(tracePath)
	span := "span"
	if usesName(body, "span") {
		span = "egoSpan"
	}
	end := "nil"
	if errName != "" {
		end = "&" + errName
	}
	text := fmt.Sprintf("\nctx, %s := %s.Start(ctx, %q)\ndefer %s.End(%s, %s)", span, tr, spanName, tr, span, end)
	g.edits = append(g.edits, edit{g.off(body.Lbrace) + 1, g.off(body.Lbrace) + 1, text})
}

func usesName(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}

// ctxEdits inserts ctx into the calls under n whose callee takes a
// context.Context first and that leave it out.
func (g *fileGen) ctxEdits(n ast.Node) {
	if g.r.ti == nil {
		return
	}
	ast.Inspect(n, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || g.builtin(call) != "" {
			return true
		}
		tv, ok := g.tv(call.Fun)
		if !ok || tv.IsType() || tv.IsBuiltin() {
			return true
		}
		sig, ok := tv.Type.Underlying().(*types.Signature)
		if !ok || !g.leavesCtx(call, sig) {
			return true
		}
		text := "ctx"
		if len(call.Args) > 0 {
			text += ", "
		}
		g.edits = append(g.edits, edit{g.off(call.Lparen) + 1, g.off(call.Lparen) + 1, text})
		return true
	})
}

// leavesCtx reports whether call leaves out the context.Context that sig
// takes first, so that ctx is passed for it.
func (g *fileGen) leavesCtx(call *ast.CallExpr, sig *types.Signature) bool {
	if sig.Params().Len() == 0 || !isContext(sig.Params().At(0).Type()) {
		return false
	}
	if len(call.Args) > 0 {
		t := g.typeOf(call.Args[0])
		if t != nil && (isContext(t) || types.Implements(t, contextIface(sig))) {
			return false // explicit
		}
		if t == nil && sig.Variadic() {
			return false // can't tell whether ctx was passed
		}
	}
	want := sig.Params().Len() - 1
	return len(call.Args) == want || sig.Variadic() && len(call.Args) >= want-1
}

func contextIface(sig *types.Signature) *types.Interface {
	return sig.Params().At(0).Type().Underlying().(*types.Interface)
}

// funcDecl renders a function declaration and its body.
func (g *fileGen) funcDecl(d *ast.FuncDecl) {
	if d.Body == nil {
		g.copy(g.off(d.Pos()), g.off(d.End()))
		return
	}
	g.copy(g.off(d.Pos()), g.off(d.Body.Pos()))
	g.pushFunc(d.Type, d.Body)
	g.node(d.Body)
	g.fn = g.fn.outer
}

func (g *fileGen) funcLit(f *ast.FuncLit) {
	g.copy(g.off(f.Pos()), g.off(f.Body.Pos()))
	g.pushFunc(f.Type, f.Body)
	g.fn.ctx = g.fn.outer != nil && g.fn.outer.ctx || hasParam(f.Type, "ctx")
	g.node(f.Body)
	g.fn = g.fn.outer
}

func hasParam(ft *ast.FuncType, name string) bool {
	for _, f := range ft.Params.List {
		for _, n := range f.Names {
			if n.Name == name {
				return true
			}
		}
	}
	return false
}

// pushFunc enters a function.
func (g *fileGen) pushFunc(ft *ast.FuncType, body ast.Node) {
	fs := &funcState{outer: g.fn, effect: ft.Effect.IsValid(), set: g.pkg.resultSet(ft), used: map[string]bool{}, temps: map[string]bool{}}
	fs.ctx = fs.effect || hasParam(ft, "ctx")
	if ft.Results != nil {
		var exprs []ast.Expr
		for _, f := range ft.Results.List {
			for range max(1, len(f.Names)) {
				exprs = append(exprs, f.Type)
			}
		}
		if n := len(exprs); n > 0 {
			last := exprs[n-1]
			if fs.set != nil || isErrorIdent(last) {
				fs.hasErr = true
				exprs = exprs[:n-1]
			}
		}
		fs.results = exprs
		for _, e := range exprs {
			fs.types = append(fs.types, g.typeOf(e))
		}
	}
	ast.Inspect(ft, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			fs.used[id.Name] = true
		}
		return true
	})
	if body != nil {
		ast.Inspect(body, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				fs.used[id.Name] = true
			}
			return true
		})
	}
	g.fn = fs
}

func isErrorIdent(x ast.Expr) bool {
	id, ok := x.(*ast.Ident)
	return ok && id.Name == "error"
}

// temp returns a fresh variable name for the current function.
func (g *fileGen) temp(base string) string {
	fs := g.fn
	if fs == nil {
		return base
	}
	for i := 1; ; i++ {
		n := base
		if i > 1 {
			n += strconv.Itoa(i)
		}
		if !fs.used[n] && !fs.temps[n] && !g.pkg.topNames[n] {
			fs.temps[n] = true
			return n
		}
	}
}

// zeros returns the zero values of the current function's results, error
// excluded, as source.
func (g *fileGen) zeros() string {
	if g.fn == nil {
		return ""
	}
	var parts []string
	for i, e := range g.fn.results {
		t := g.fn.types[i]
		if t == nil {
			t = g.typeOf(e)
		}
		if t == nil {
			parts = append(parts, "*new("+g.text(e)+")")
			continue
		}
		parts = append(parts, g.zero(t))
	}
	return strings.Join(parts, ", ")
}

// ret returns a return statement with zero values and the error err.
func (g *fileGen) ret(err string) string {
	z := g.zeros()
	if z == "" {
		return "return " + err
	}
	return "return " + z + ", " + err
}

func isStd(p string) bool { return !strings.Contains(strings.SplitN(p, "/", 2)[0], ".") }

func nonStd(paths []string) []string {
	var out []string
	for _, p := range paths {
		if !isStd(p) {
			out = append(out, p)
		}
	}
	return out
}

// exprPatterns returns the patterns of match expressions.
func (g *fileGen) exprPatterns() map[*ast.CallExpr]bool {
	if g.exprPats == nil {
		g.exprPats = map[*ast.CallExpr]bool{}
		ast.Inspect(g.file, func(n ast.Node) bool {
			if m, ok := n.(*ast.MatchExpr); ok {
				for _, a := range m.Arms {
					for _, p := range a.Patterns {
						if c, ok := p.(*ast.CallExpr); ok {
							g.exprPats[c] = true
						}
					}
				}
			}
			return true
		})
	}
	return g.exprPats
}

func declDoc(d ast.Decl) *ast.CommentGroup {
	switch d := d.(type) {
	case *ast.FuncDecl:
		return d.Doc
	case *ast.GenDecl:
		return d.Doc
	case *ast.SumDecl:
		return d.Doc
	}
	return nil
}

// runtimePkgs are the effect-go packages a .ego file may use without
// importing them.
var runtimePkgs = map[string]string{
	"scope":    scopePath,
	"schedule": schedulePath,
	"trace":    tracePath,
	"layer":    modPath + "/layer",
}

// autoImports imports the runtime packages the file refers to without
// importing them.
func (g *fileGen) autoImports() {
	ast.Inspect(g.file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Obj != nil || g.pkg.topNames[id.Name] || g.taken[id.Name] {
			return true
		}
		if p, ok := runtimePkgs[id.Name]; ok {
			g.use(p, id.Name)
		}
		return true
	})
}

// importBlock writes an import block with the added paths and the file's
// own import, if any: the standard library first, then the rest.
func (g *fileGen) importBlock(paths []string, spec func(string) string, own *ast.ImportSpec) {
	ownStd := false
	if own != nil {
		p, _ := strconv.Unquote(own.Path.Value)
		ownStd = isStd(p)
	}
	writeOwn := func() {
		g.w.str("\t")
		g.copy(g.off(own.Pos()), g.off(own.End()))
		g.w.str("\n")
	}
	g.w.str("import (\n")
	if own != nil && ownStd {
		writeOwn()
	}
	for _, p := range paths {
		if isStd(p) {
			g.w.str("\t" + spec(p) + "\n")
		}
	}
	others := nonStd(paths)
	if len(others) > 0 || own != nil && !ownStd {
		g.w.str("\n")
		if own != nil && !ownStd {
			writeOwn()
		}
		for _, p := range others {
			g.w.str("\t" + spec(p) + "\n")
		}
	}
	g.w.str(")")
}
