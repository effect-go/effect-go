// Package layers generates the wiring for layer.Build injectors: plain Go
// that calls each provider once, in dependency order, and registers
// finalizers on the scope.
package layers

import (
	"bytes"
	"fmt"
	"github.com/effect-go/effect-go/internal/typeutil"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/tools/go/packages"
)

const (
	layerPath = "github.com/effect-go/effect-go/layer"
	scopePath = "github.com/effect-go/effect-go/scope"
	// Tag is the build tag of injector files.
	Tag = "egolayers"
	// FileName is the generated file.
	FileName = "layers_ego.go"
)

// A Diagnostic is an error in an injector.
type Diagnostic struct {
	Pos token.Position
	Msg string
}

func (d Diagnostic) String() string { return fmt.Sprintf("%v: %s", d.Pos, d.Msg) }

// Result is the generated file, or the reasons there is none.
type Result struct {
	Path  string
	Code  []byte
	Diags []Diagnostic
}

// HasInjectors reports whether dir has injector files.
func HasInjectors(dir string) bool { return len(InjectorFiles(dir)) > 0 }

// InjectorFiles returns the paths of dir's injector files.
func InjectorFiles(dir string) []string {
	var paths []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if path := filepath.Join(dir, e.Name()); strings.HasSuffix(e.Name(), ".go") && isInjectorFile(path) {
			paths = append(paths, path)
		}
	}
	return paths
}

func isInjectorFile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			return false
		}
		if strings.HasPrefix(line, "//go:build ") && strings.TrimSpace(strings.TrimPrefix(line, "//go:build ")) == Tag {
			return true
		}
	}
	return false
}

// Generate writes nothing: it returns the wiring for the injectors in dir,
// or nil if there are none.
func Generate(dir string) (*Result, error) {
	if !HasInjectors(dir) {
		return nil, nil
	}
	cfg := &packages.Config{
		Mode:       packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
		Dir:        dir,
		BuildFlags: []string{"-tags=" + strings.Join(append(goflagsTags(), Tag), ",")},
	}
	// The package, then also the packages declaring the sets it uses, until
	// every set's declaration is loaded: in one load, so they share types.
	patterns := []string{"."}
	root := "" // the package's import path, from the first load
	for {
		pkgs, err := packages.Load(cfg, patterns...)
		if err != nil {
			return nil, err
		}
		if root == "" {
			if len(pkgs) != 1 {
				return nil, fmt.Errorf("%s: expected one package", dir)
			}
			root = pkgs[0].PkgPath
		}
		var pkg *packages.Package
		for _, p := range pkgs {
			if p.PkgPath == root {
				pkg = p
			}
		}
		if pkg == nil {
			return nil, fmt.Errorf("%s: package %s not loaded", dir, root)
		}
		res := &Result{Path: filepath.Join(dir, FileName)}
		if len(pkg.Errors) > 0 {
			for _, e := range pkg.Errors {
				res.Diags = append(res.Diags, Diagnostic{Pos: parsePos(e.Pos), Msg: e.Msg})
			}
			return res, nil
		}
		g := &gen{pkg: pkg, all: pkgs, imports: map[string]string{}, locals: map[string]bool{}, need: map[string]bool{}}
		code, ok := g.generate(res)
		more := false
		for path := range g.need {
			if !slices.Contains(patterns, path) {
				patterns, more = append(patterns, path), true
			}
		}
		if more {
			continue
		}
		for path := range g.need {
			res.Diags = append(res.Diags, Diagnostic{Pos: pkg.Fset.Position(pkg.Syntax[0].Package), Msg: "can't load " + path + ", which declares a layer.Set used here"})
		}
		if ok && len(g.need) == 0 {
			res.Code = code
		}
		return res, nil
	}
}

// goflagsTags returns the build tags of GOFLAGS, where ego generate -tags
// puts them: a -tags flag would replace them.
func goflagsTags() []string {
	var tags []string
	for _, f := range strings.Fields(os.Getenv("GOFLAGS")) {
		if t, ok := strings.CutPrefix(strings.TrimLeft(f, "-"), "tags="); ok {
			tags = strings.FieldsFunc(t, func(r rune) bool { return r == ',' })
		}
	}
	return tags
}

// generate renders the wiring of g.pkg's injectors, reporting whether it
// could: with diagnostics, or with sets declared in packages not loaded yet
// (in g.need), it couldn't.
func (g *gen) generate(res *Result) ([]byte, bool) {
	pkg := g.pkg
	var bodies []string
	var injectorFiles []*ast.File
	for _, f := range pkg.Syntax {
		if !isInjectorFile(pkg.Fset.File(f.Pos()).Name()) {
			continue
		}
		injectorFiles = append(injectorFiles, f)
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			args, ok := g.buildCall(fd)
			if !ok {
				continue
			}
			g.file = f
			if body, ok := g.injector(fd, args); ok {
				bodies = append(bodies, body)
			}
		}
	}
	res.Diags = g.diags
	if len(res.Diags) > 0 || len(g.need) > 0 {
		return nil, false
	}
	var b bytes.Buffer
	b.WriteString("// Code generated by ego generate. DO NOT EDIT.\n\n//go:build !" + Tag + "\n\npackage " + pkg.Name + "\n\n")
	if len(g.imports) > 0 {
		b.WriteString("import (\n")
		var paths []string
		for p := range g.imports {
			paths = append(paths, p)
		}
		// The standard library first, as goimports groups them.
		sort.Slice(paths, func(i, j int) bool {
			if si, sj := isStd(paths[i]), isStd(paths[j]); si != sj {
				return si
			}
			return paths[i] < paths[j]
		})
		blank := false
		for _, p := range paths {
			if !isStd(p) && !blank {
				b.WriteString("\n")
				blank = true
			}
			if g.imports[p] != path.Base(p) {
				b.WriteString(g.imports[p] + " ")
			}
			b.WriteString(strconv.Quote(p) + "\n")
		}
		b.WriteString(")\n\n")
	}
	b.WriteString(strings.Join(bodies, "\n"))
	code, err := format.Source(b.Bytes())
	if err != nil {
		g.errorf(token.NoPos, "internal error: %v\n%s", err, b.Bytes())
		res.Diags = g.diags
		return nil, false
	}
	return code, true
}

func isStd(path string) bool { return !strings.Contains(strings.SplitN(path, "/", 2)[0], ".") }

func parsePos(s string) token.Position {
	parts := strings.Split(s, ":")
	p := token.Position{Filename: parts[0]}
	if len(parts) > 1 {
		p.Line, _ = strconv.Atoi(parts[1])
	}
	if len(parts) > 2 {
		p.Column, _ = strconv.Atoi(parts[2])
	}
	return p
}

type gen struct {
	pkg     *packages.Package
	all     []*packages.Package // pkg, and those declaring the sets it uses
	file    *ast.File
	imports map[string]string // path -> name
	locals  map[string]bool   // the injectors' parameters and variables
	diags   []Diagnostic
	need    map[string]bool // packages declaring sets, to load
}

// info returns the type information of the package x is written in.
func (g *gen) info(x ast.Node) *types.Info {
	for _, p := range g.all {
		for _, f := range p.Syntax {
			if f.FileStart <= x.Pos() && x.Pos() <= f.FileEnd {
				return p.TypesInfo
			}
		}
	}
	return g.pkg.TypesInfo
}

func (g *gen) errorf(pos token.Pos, format string, args ...any) {
	g.diags = append(g.diags, Diagnostic{Pos: g.pkg.Fset.Position(pos), Msg: fmt.Sprintf(format, args...)})
}

// buildCall returns the arguments of panic(layer.Build(...)) if fd's body is
// just that.
func (g *gen) buildCall(fd *ast.FuncDecl) ([]ast.Expr, bool) {
	if len(fd.Body.List) != 1 {
		return nil, false
	}
	es, ok := fd.Body.List[0].(*ast.ExprStmt)
	if !ok {
		return nil, false
	}
	p, ok := es.X.(*ast.CallExpr)
	if !ok || len(p.Args) != 1 {
		return nil, false
	}
	if id, ok := p.Fun.(*ast.Ident); !ok || id.Name != "panic" {
		return nil, false
	}
	c, ok := p.Args[0].(*ast.CallExpr)
	if !ok || !g.isLayerFunc(c.Fun, "Build") {
		return nil, false
	}
	return c.Args, true
}

func (g *gen) isLayerFunc(x ast.Expr, name string) bool {
	var id *ast.Ident
	switch x := x.(type) {
	case *ast.SelectorExpr:
		id = x.Sel
	case *ast.Ident:
		id = x
	default:
		return false
	}
	f, ok := g.info(id).Uses[id].(*types.Func)
	return ok && f.Pkg() != nil && f.Pkg().Path() == layerPath && f.Name() == name
}

// A provider is a constructor in the graph.
type provider struct {
	expr    ast.Expr     // as written
	obj     types.Object // the function, or nil for a method value
	sig     *types.Signature
	out     types.Type
	hasErr  bool
	cleanup string // "", "func()", "func() error", "func(ctx) error"
	close   bool   // layer.Close: call out.Close when the scope closes
	direct  bool   // passed to Build itself, not through a Set
	inner   int    // layer.Decorate: the parameter that takes the service it wraps; else -1
	used    bool
}

func (p *provider) name() string { return types.ExprString(p.expr) }

// providers collects the providers in args.
func (g *gen) providers(args []ast.Expr, direct bool, seen map[types.Object]bool) []*provider {
	var out []*provider
	for _, a := range args {
		a = ast.Unparen(a)
		if c, ok := a.(*ast.CallExpr); ok && g.isLayerFunc(c.Fun, "Close") && len(c.Args) == 1 {
			for _, p := range g.providers(c.Args, direct, seen) {
				p.close = true
				if !hasClose(p.out) {
					g.errorf(c.Pos(), "layer.Close: %s has no Close method", g.typeString(p.out))
				}
				out = append(out, p)
			}
			continue
		}
		if c, ok := a.(*ast.CallExpr); ok && g.isLayerFunc(c.Fun, "Decorate") && len(c.Args) == 1 {
			for _, p := range g.providers(c.Args, direct, seen) {
				for i := range p.sig.Params().Len() {
					if types.Identical(p.sig.Params().At(i).Type(), p.out) {
						p.inner = i
					}
				}
				if p.inner < 0 {
					g.errorf(c.Pos(), "layer.Decorate: %s takes no %s to wrap", p.name(), g.typeString(p.out))
					continue
				}
				out = append(out, p)
			}
			continue
		}
		if c, ok := a.(*ast.CallExpr); ok && g.isLayerFunc(c.Fun, "Set") {
			out = append(out, g.providers(c.Args, false, seen)...)
			continue
		}
		obj := g.objectOf(a)
		if v, ok := obj.(*types.Var); ok && isProviderSet(v.Type()) {
			if seen[v] {
				continue
			}
			seen[v] = true
			init := g.varInit(v)
			if init == nil && !g.loaded(v.Pkg()) {
				g.need[v.Pkg().Path()] = true // load it, and try again
				continue
			}
			if init == nil {
				g.errorf(a.Pos(), "can't find the layer.Set that %s is declared with", v.Name())
				continue
			}
			out = append(out, g.providers([]ast.Expr{init}, false, seen)...)
			continue
		}
		if !isName(a) {
			g.errorf(a.Pos(), "%s: a provider is a function's name, such as NewDB or sql.Open", types.ExprString(a))
			continue
		}
		t := g.info(a).TypeOf(a)
		sig, ok := t.(*types.Signature)
		if !ok {
			g.errorf(a.Pos(), "%s is not a provider function", types.ExprString(a))
			continue
		}
		p := &provider{expr: a, sig: sig, direct: direct, inner: -1}
		if f, ok := obj.(*types.Func); !ok || f.Signature().Recv() == nil {
			p.obj = obj
		}
		rs := sig.Results()
		switch {
		case rs.Len() == 1:
			p.out = rs.At(0).Type()
		case rs.Len() == 2 && isError(rs.At(1).Type()):
			p.out, p.hasErr = rs.At(0).Type(), true
		case rs.Len() == 3 && isError(rs.At(2).Type()) && cleanupKind(rs.At(1).Type()) != "":
			p.out, p.hasErr, p.cleanup = rs.At(0).Type(), true, cleanupKind(rs.At(1).Type())
		default:
			g.errorf(a.Pos(), "%s: a provider returns T, (T, error) or (T, cleanup, error)", types.ExprString(a))
			continue
		}
		out = append(out, p)
	}
	return out
}

func (g *gen) objectOf(x ast.Expr) types.Object {
	switch x := x.(type) {
	case *ast.Ident:
		return g.info(x).Uses[x]
	case *ast.SelectorExpr:
		return g.info(x).Uses[x.Sel]
	case *ast.IndexExpr:
		return g.objectOf(x.X)
	case *ast.IndexListExpr:
		return g.objectOf(x.X)
	}
	return nil
}

// isName reports whether x names a function, as a provider must: NewDB,
// db.Open, or an instance of a generic function, NewCache[Item].
func isName(x ast.Expr) bool {
	switch e := x.(type) {
	case *ast.IndexExpr:
		x = e.X
	case *ast.IndexListExpr:
		x = e.X
	}
	switch x := x.(type) {
	case *ast.Ident:
		return true
	case *ast.SelectorExpr:
		_, ok := x.X.(*ast.Ident)
		return ok
	}
	return false
}

// loaded reports whether p's syntax is loaded.
func (g *gen) loaded(p *types.Package) bool {
	for _, lp := range g.all {
		if lp.Types == p && len(lp.Syntax) > 0 {
			return true
		}
	}
	return false
}

func (g *gen) varInit(v *types.Var) ast.Expr {
	for _, p := range g.all {
		if p.Types != v.Pkg() {
			continue
		}
		for _, f := range p.Syntax {
			for _, d := range f.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok || gd.Tok != token.VAR {
					continue
				}
				for _, s := range gd.Specs {
					vs := s.(*ast.ValueSpec)
					for i, n := range vs.Names {
						if p.TypesInfo.Defs[n] == v && i < len(vs.Values) {
							return vs.Values[i]
						}
					}
				}
			}
		}
	}
	return nil
}

func isProviderSet(t types.Type) bool { return typeutil.IsNamed(t, layerPath, "ProviderSet") }

var (
	isError   = typeutil.IsError
	isContext = typeutil.IsContext
)

func isScope(t types.Type) bool {
	p, ok := t.(*types.Pointer)
	return ok && typeutil.IsNamed(p.Elem(), scopePath, "Scope")
}

func cleanupKind(t types.Type) string {
	sig, ok := t.Underlying().(*types.Signature)
	if !ok || sig.Variadic() {
		return ""
	}
	switch {
	case sig.Params().Len() == 0 && sig.Results().Len() == 0:
		return "func()"
	case sig.Params().Len() == 0 && sig.Results().Len() == 1 && isError(sig.Results().At(0).Type()):
		return "func() error"
	case sig.Params().Len() == 1 && isContext(sig.Params().At(0).Type()) && sig.Results().Len() == 1 && isError(sig.Results().At(0).Type()):
		return "func(ctx) error"
	}
	return ""
}

func hasClose(t types.Type) bool {
	obj, _, _ := types.LookupFieldOrMethod(t, true, nil, "Close")
	f, ok := obj.(*types.Func)
	if !ok {
		return false
	}
	sig := f.Signature()
	return sig.Params().Len() == 0 && (sig.Results().Len() == 0 || sig.Results().Len() == 1 && isError(sig.Results().At(0).Type()))
}

func closeReturnsError(t types.Type) bool {
	obj, _, _ := types.LookupFieldOrMethod(t, true, nil, "Close")
	f, ok := obj.(*types.Func) // no Close is reported where layer.Close is used
	return ok && f.Signature().Results().Len() == 1
}

// injector generates one injector function.
func (g *gen) injector(fd *ast.FuncDecl, args []ast.Expr) (string, bool) {
	before := len(g.diags)
	name := fd.Name.Name
	obj := g.pkg.TypesInfo.Defs[fd.Name].(*types.Func)
	sig := obj.Signature()
	if sig.Results().Len() != 2 || !isError(sig.Results().At(1).Type()) {
		g.errorf(fd.Pos(), "%s: an injector returns (T, error)", name)
		return "", false
	}
	want := sig.Results().At(0).Type()
	// The same function in several sets is one provider.
	var ps []*provider
	for _, p := range g.providers(args, true, map[types.Object]bool{}) {
		i := slices.IndexFunc(ps, func(q *provider) bool {
			return p.obj != nil && q.obj == p.obj && types.Identical(q.sig, p.sig) && (q.inner < 0) == (p.inner < 0)
		})
		if i < 0 {
			ps = append(ps, p)
			continue
		}
		if p.direct {
			ps[i].expr, ps[i].direct = p.expr, true
		}
		ps[i].close = ps[i].close || p.close
	}
	if len(g.diags) > before {
		return "", false // the graph would only add errors, about providers it lacks
	}

	b := &builder{g: g, fn: name, pos: fd.Body.List[0].Pos(), names: map[string]bool{}, building: map[*provider]bool{}, decorating: map[*provider]bool{}}
	b.top = &frame{}
	b.f = b.top
	// Decorators wrap what the other providers give, in the order listed.
	ps = slices.DeleteFunc(ps, func(p *provider) bool {
		if p.inner >= 0 {
			b.decorators = append(b.decorators, p)
		}
		return p.inner >= 0
	})
	// The injector's parameters are inputs.
	for i := 0; i < sig.Params().Len(); i++ {
		v := sig.Params().At(i)
		pname := v.Name()
		if pname == "" || pname == "_" {
			g.errorf(fd.Pos(), "%s: name every parameter", name)
			return "", false
		}
		b.declare(pname)
		b.inputs = append(b.inputs, input{pname, v.Type()})
		if isContext(v.Type()) {
			b.ctx = pname
		}
		if isScope(v.Type()) {
			b.scope = pname
		}
	}
	b.top.zero = g.zero(want) // after the parameters, so it names packages they don't hide
	// Direct providers take precedence over those from sets. Types are
	// compared with Identical, which sees through aliases.
	var byType [][]*provider
	for _, p := range ps {
		if i := slices.IndexFunc(byType, func(list []*provider) bool { return types.Identical(list[0].out, p.out) }); i >= 0 {
			byType[i] = append(byType[i], p)
		} else {
			byType = append(byType, []*provider{p})
		}
	}
	for _, list := range byType {
		var direct []*provider
		for _, p := range list {
			if p.direct {
				direct = append(direct, p)
			}
		}
		if len(direct) > 0 {
			list = direct
		}
		if len(list) > 1 {
			var names []string
			for _, p := range list {
				names = append(names, p.name())
			}
			g.errorf(fd.Pos(), "%s: several providers for %s: %s", name, g.typeString(types.Unalias(list[0].out)), strings.Join(names, ", "))
			return "", false
		}
		b.providers = append(b.providers, list[0])
	}
	sort.Slice(b.providers, func(i, j int) bool { return b.providers[i].expr.Pos() < b.providers[j].expr.Pos() })
	for _, d := range b.decorators {
		if !b.canProvide(d.out) {
			g.errorf(d.expr.Pos(), "%s: %s wraps %s, which no provider gives", name, d.name(), g.typeString(d.out))
		}
	}
	if len(g.diags) > before {
		return "", false
	}

	result := b.resolve(want, "the result of "+name, nil)
	if len(g.diags) > before {
		return "", false
	}
	for _, p := range append(slices.Clone(b.providers), b.decorators...) {
		if p.direct && !p.used {
			g.errorf(p.expr.Pos(), "%s: %s is not needed", name, p.name())
		}
	}
	if len(g.diags) > before {
		return "", false
	}

	var out strings.Builder
	if fd.Doc != nil {
		for _, c := range fd.Doc.List {
			out.WriteString(c.Text + "\n")
		}
	}
	out.WriteString("func " + name + "(")
	for i, in := range b.inputs {
		if i > 0 {
			out.WriteString(", ")
		}
		out.WriteString(in.name + " " + g.typeString(in.typ))
	}
	out.WriteString(") (" + g.typeString(want) + ", error) {\n")
	out.WriteString(b.top.body.String())
	out.WriteString("return " + result + ", nil\n}\n")
	return out.String(), true
}

type input struct {
	name string
	typ  types.Type
}

// builder writes the body of one injector.
type builder struct {
	g          *gen
	fn         string
	pos        token.Pos // the layer.Build call, where graph errors are reported
	inputs     []input
	ctx        string
	scope      string
	providers  []*provider
	decorators []*provider // in the order listed
	top, f     *frame      // the injector's body, and the one being written
	cells      []value     // the layer.Lazy of each type that has one
	names      map[string]bool
	building   map[*provider]bool
	decorating map[*provider]bool
	lazies     []types.Type // the lazy services being written
}

// A frame is a function the builder writes: the injector, or the build
// function of a lazy service.
type frame struct {
	body strings.Builder
	zero string  // the zero values of its results but the error
	vals []value // built in it
}

// A value is the variable holding a type built already.
type value struct {
	typ  types.Type
	name string
}

// built returns the variable holding a value of type t, if it is an input
// or built in the current frame or, before it, in the injector's.
func (b *builder) built(t types.Type) (string, bool) {
	for _, in := range b.inputs {
		if types.Identical(in.typ, t) {
			return in.name, true
		}
	}
	for _, f := range []*frame{b.f, b.top} {
		for _, v := range f.vals {
			if types.Identical(v.typ, t) {
				return v.name, true
			}
		}
	}
	return "", false
}

// provides reports whether an input or a provider has type t itself.
func (b *builder) provides(t types.Type) bool {
	return slices.ContainsFunc(b.inputs, func(in input) bool { return types.Identical(in.typ, t) }) ||
		slices.ContainsFunc(b.providers, func(q *provider) bool { return types.Identical(q.out, t) })
}

// canProvide reports whether a provider gives t: has type t, or implements
// it if t is an interface.
func (b *builder) canProvide(t types.Type) bool {
	iface, isIface := t.Underlying().(*types.Interface)
	return slices.ContainsFunc(b.providers, func(q *provider) bool {
		return types.Identical(q.out, t) || isIface && types.Implements(q.out, iface)
	})
}

// resolve returns the variable holding a value of type t, building it. In a
// lazy service's build function, what isn't built before it comes from
// the lazy service of its own: each provider runs once.
func (b *builder) resolve(t types.Type, neededBy string, chain []string) string {
	if v, ok := b.built(t); ok {
		return v
	}
	cell, ok := b.cell(t)
	if !ok && b.f == b.top {
		return b.make(t, neededBy, chain)
	}
	if !ok {
		if cell = b.lazy(t, neededBy, chain); cell == "" {
			return "nil"
		}
	}
	v := b.varName(t)
	fmt.Fprintf(&b.f.body, "%s, err := %s.Get()\nif err != nil {\nreturn %s, err\n}\n", v, cell, b.f.zero)
	b.f.vals = append(b.f.vals, value{t, v})
	return v
}

// cell returns the layer.Lazy of type t, if it has one.
func (b *builder) cell(t types.Type) (string, bool) {
	for _, c := range b.cells {
		if types.Identical(c.typ, t) {
			return c.name, true
		}
	}
	return "", false
}

// lazy returns the layer.Lazy of type t, declaring it in the injector's
// body if needed. It returns "" after reporting an error.
func (b *builder) lazy(t types.Type, neededBy string, chain []string) string {
	if c, ok := b.cell(t); ok {
		return c
	}
	if slices.ContainsFunc(b.lazies, func(u types.Type) bool { return types.Identical(u, t) }) {
		b.g.errorf(b.pos, "%s: dependency cycle: %s", b.fn, strings.Join(append(chain, b.g.typeString(t)), " → "))
		return ""
	}
	b.lazies = append(b.lazies, t)
	saved := b.f
	b.f = &frame{zero: b.g.zero(t)}
	v, ok := b.built(t) // an input, or in the injector already
	if !ok {
		v = b.make(t, neededBy, chain)
	}
	body := b.f.body.String()
	b.f = saved
	b.lazies = b.lazies[:len(b.lazies)-1]
	name, named := "lazy", t
	if p, ok := t.(*types.Pointer); ok {
		named = p.Elem()
	}
	if n, ok := types.Unalias(named).(*types.Named); ok {
		name += n.Obj().Name() // lazyDB
	}
	name = b.fresh(name)
	fmt.Fprintf(&b.top.body, "%s := %s.NewLazy(func() (%s, error) {\n%sreturn %s, nil\n})\n", name, b.g.pkgName(layerPath, "layer"), b.g.typeString(t), body, v)
	b.cells = append(b.cells, value{t, name})
	return name
}

// make builds a value of type t in the current frame: with its provider,
// then its decorators.
func (b *builder) make(t types.Type, neededBy string, chain []string) string {
	var p *provider
	for _, q := range b.providers {
		if types.Identical(q.out, t) {
			p = q
		}
	}
	// A provider of a type implementing an interface provides it too. A
	// direct one wins even over a set's provider of the interface itself,
	// so that layer.Build(AppSet, NewMemRepo) swaps it.
	if iface, ok := t.Underlying().(*types.Interface); ok && (p == nil || !p.direct) {
		var cands, direct []*provider
		for _, q := range b.providers {
			if types.Implements(q.out, iface) {
				cands = append(cands, q)
				if q.direct {
					direct = append(direct, q)
				}
			}
		}
		if p == nil {
			// The injector's inputs win, as for exact types.
			var ins []string
			for _, in := range b.inputs {
				if types.Implements(in.typ, iface) && !isContext(in.typ) {
					ins = append(ins, in.name)
				}
			}
			switch len(ins) {
			case 0:
			case 1:
				return ins[0]
			default:
				b.g.errorf(b.pos, "%s: several inputs implement %s, needed by %s: %s", b.fn, b.g.typeString(t), neededBy, strings.Join(ins, ", "))
				return "nil"
			}
		}
		// Direct providers win, as for exact types.
		switch {
		case len(direct) > 0:
			cands = direct
		case p != nil:
			cands = []*provider{p}
		}
		switch len(cands) {
		case 1:
			p = cands[0]
		case 0:
		default:
			var names []string
			for _, q := range cands {
				names = append(names, q.name())
			}
			b.g.errorf(b.pos, "%s: several providers implement %s, needed by %s: %s", b.fn, b.g.typeString(t), neededBy, strings.Join(names, ", "))
			return "nil"
		}
	}
	if p == nil {
		b.g.errorf(b.pos, "%s: no provider for %s, needed by %s", b.fn, b.g.typeString(t), neededBy)
		return "nil"
	}
	if !types.Identical(p.out, t) {
		// Found through an interface: built as its own type, then wrapped
		// by the interface's decorators.
		v := b.decorate(t, b.resolve(p.out, neededBy, chain), chain)
		b.f.vals = append(b.f.vals, value{t, v})
		return v
	}
	if b.building[p] {
		b.g.errorf(b.pos, "%s: dependency cycle: %s", b.fn, strings.Join(append(chain, p.name()), " → "))
		return "nil"
	}
	b.building[p] = true
	v := b.call(p, b.args(p, chain, ""))
	v = b.decorate(t, v, chain) // still building p: a decorator needing t is a cycle
	delete(b.building, p)
	b.f.vals = append(b.f.vals, value{t, v})
	return v
}

// decorate wraps v, of type t, with t's decorators, the first listed
// innermost.
func (b *builder) decorate(t types.Type, v string, chain []string) string {
	for _, d := range b.decorators {
		if !types.Identical(d.out, t) {
			continue
		}
		if b.decorating[d] {
			b.g.errorf(b.pos, "%s: dependency cycle: %s", b.fn, strings.Join(append(chain, d.name()), " → "))
			return "nil"
		}
		b.decorating[d] = true
		v = b.call(d, b.args(d, chain, v))
		delete(b.decorating, d)
	}
	return v
}

// args returns the arguments of a provider's call: a decorator's inner
// parameter takes inner.
func (b *builder) args(p *provider, chain []string, inner string) []string {
	p.used = true
	chain = append(chain, p.name())
	var args []string
	for i := 0; i < p.sig.Params().Len(); i++ {
		pt := p.sig.Params().At(i).Type()
		switch {
		case i == p.inner:
			args = append(args, inner)
		case isContext(pt) && b.ctx != "":
			args = append(args, b.ctx)
		case isScope(pt) && b.scope != "":
			args = append(args, b.scope)
		case isLazy(pt):
			c := b.lazy(pt.(*types.Named).TypeArgs().At(0), p.name(), chain)
			if c == "" {
				c = "nil"
			}
			args = append(args, c)
		case p.sig.Variadic() && i == p.sig.Params().Len()-1:
			// ...T takes the []T provided, if any, and else nothing.
			if b.provides(pt) {
				args = append(args, b.resolve(pt, p.name(), chain)+"...")
			}
		default:
			args = append(args, b.resolve(pt, p.name(), chain))
		}
	}
	return args
}

// call writes the call of a provider in the current frame, with its error
// and its finalizer, and returns the variable holding its result.
func (b *builder) call(p *provider, args []string) string {
	// The packages before the variable, which then doesn't take their names.
	call := b.g.exprString(p.expr) + "(" + strings.Join(args, ", ") + ")"
	var wrap, ctx string
	if p.hasErr {
		wrap = b.g.pkgName("fmt", "fmt") + ".Errorf(\"" + p.name() + ": %w\", err)"
	}
	if p.close || p.cleanup == "func()" || p.cleanup == "func() error" {
		ctx = b.g.pkgName("context", "context")
	}
	v := b.varName(p.out)
	needsScope := p.close || p.cleanup != ""
	if needsScope && b.scope == "" {
		b.g.errorf(p.expr.Pos(), "%s: %s has a finalizer, so the injector needs a *scope.Scope parameter", b.fn, p.name())
	}
	body, zero := &b.f.body, b.f.zero
	switch {
	case p.cleanup != "":
		c := b.fresh("cleanup")
		fmt.Fprintf(body, "%s, %s, err := %s\nif err != nil {\nreturn %s, %s\n}\n", v, c, call, zero, wrap)
		switch p.cleanup {
		case "func()":
			fmt.Fprintf(body, "%s.Defer(func(%s.Context) error { %s(); return nil })\n", b.scope, ctx, c)
		case "func() error":
			fmt.Fprintf(body, "%s.Defer(func(%s.Context) error { return %s() })\n", b.scope, ctx, c)
		default:
			fmt.Fprintf(body, "%s.Defer(%s)\n", b.scope, c)
		}
	case p.hasErr:
		fmt.Fprintf(body, "%s, err := %s\nif err != nil {\nreturn %s, %s\n}\n", v, call, zero, wrap)
	default:
		fmt.Fprintf(body, "%s := %s\n", v, call)
	}
	if p.close {
		if closeReturnsError(p.out) {
			fmt.Fprintf(body, "%s.Defer(func(%s.Context) error { return %s.Close() })\n", b.scope, ctx, v)
		} else {
			fmt.Fprintf(body, "%s.Defer(func(%s.Context) error { %s.Close(); return nil })\n", b.scope, ctx, v)
		}
	}
	return v
}

// isLazy reports whether t is a layer.Lazy[T].
func isLazy(t types.Type) bool {
	n, ok := t.(*types.Named)
	return ok && n.TypeArgs().Len() == 1 && typeutil.IsNamed(n, layerPath, "Lazy")
}

// varName names a variable after its type: *sql.DB -> db.
func (b *builder) varName(t types.Type) string { return b.fresh(b.baseName(t)) }

// baseName is the name varName starts from.
func (b *builder) baseName(t types.Type) string {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	base := "v"
	if n, ok := types.Unalias(t).(*types.Named); ok {
		r := []rune(n.Obj().Name())
		// Lowercase the leading capitals: DB -> db, HTTPClient -> httpClient.
		i := 0
		for i < len(r) && unicode.IsUpper(r[i]) && (i == 0 || i+1 >= len(r) || unicode.IsUpper(r[i+1])) {
			r[i] = unicode.ToLower(r[i])
			i++
		}
		base = string(r)
	}
	return base
}

func (b *builder) fresh(base string) string {
	n := base
	for i := 2; b.names[n] || token.IsKeyword(n) || n == "err" || types.Universe.Lookup(n) != nil || b.g.pkg.Types.Scope().Lookup(n) != nil || b.g.isPkgName(n); i++ {
		n = base + strconv.Itoa(i)
	}
	b.declare(n)
	return n
}

// declare reserves a parameter's or a variable's name.
func (b *builder) declare(n string) {
	b.names[n] = true
	b.g.locals[n] = true
}

// exprString renders a provider expression (a name, as isName checks),
// keeping its package qualifiers imported.
func (g *gen) exprString(x ast.Expr) string {
	switch x := x.(type) {
	case *ast.IndexExpr:
		return g.exprString(x.X) + "[" + g.typeString(g.info(x).TypeOf(x.Index)) + "]"
	case *ast.IndexListExpr:
		var args []string
		for _, a := range x.Indices {
			args = append(args, g.typeString(g.info(a).TypeOf(a)))
		}
		return g.exprString(x.X) + "[" + strings.Join(args, ", ") + "]"
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			if pn, ok := g.info(id).Uses[id].(*types.PkgName); ok {
				return g.pkgName(pn.Imported().Path(), pn.Imported().Name()) + "." + x.Sel.Name
			}
		}
		return g.exprString(x.X) + "." + x.Sel.Name
	case *ast.Ident:
		// A name from a set declared in another package is qualified with it.
		if obj := g.info(x).Uses[x]; obj != nil && obj.Pkg() != nil && obj.Pkg() != g.pkg.Types {
			if !obj.Exported() {
				g.errorf(x.Pos(), "%s is unexported in package %s, so another package's injector can't call it", x.Name, obj.Pkg().Name())
			}
			return g.pkgName(obj.Pkg().Path(), obj.Pkg().Name()) + "." + x.Name
		}
	}
	return types.ExprString(x)
}

// isPkgName reports whether the generated code may refer to a package as n:
// a variable named n would hide it.
func (g *gen) isPkgName(n string) bool {
	for _, imp := range g.pkg.Types.Imports() {
		if imp.Name() == n {
			return true
		}
	}
	for _, name := range g.imports {
		if name == n {
			return true
		}
	}
	return false
}

// pkgName returns the name to refer to the package at path p, named name,
// importing it: the name the injector file, or else another file of the
// package, imports it as, or else its own, made unique in the file.
func (g *gen) pkgName(p, name string) string {
	if n, ok := g.imports[p]; ok {
		return n
	}
	for _, f := range append([]*ast.File{g.file}, g.pkg.Syntax...) {
		if a := alias(f, p); a != "" {
			name = a
			break
		}
	}
	taken := func(n string) bool {
		for _, m := range g.imports {
			if m == n {
				return true
			}
		}
		return g.locals[n] || g.pkg.Types.Scope().Lookup(n) != nil
	}
	base := name
	for i := 2; taken(name); i++ {
		name = base + strconv.Itoa(i)
	}
	g.imports[p] = name
	return name
}

// alias returns the name f imports path p as, if it names it.
func alias(f *ast.File, p string) string {
	if f == nil {
		return ""
	}
	for _, s := range f.Imports {
		if s.Name != nil && s.Name.Name != "_" && s.Name.Name != "." && strings.Trim(s.Path.Value, `"`) == p {
			return s.Name.Name
		}
	}
	return ""
}

func (g *gen) typeString(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string {
		if p == g.pkg.Types {
			return ""
		}
		return g.pkgName(p.Path(), p.Name())
	})
}

func (g *gen) zero(t types.Type) string { return typeutil.Zero(t, g.typeString) }
