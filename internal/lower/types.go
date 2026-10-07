package lower

import (
	"fmt"
	goast "go/ast"
	"go/build"
	"go/token"
	"go/types"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/tools/go/packages"

	"github.com/effect-go/effect-go/internal/syntax/ast"
	"github.com/effect-go/effect-go/internal/syntax/parser"
	"github.com/effect-go/effect-go/internal/typeutil"
)

// An Importer loads the packages a package imports, from export data, and
// caches them. It's safe for concurrent use.
type Importer struct {
	mu     sync.Mutex
	paths  []string // everything loaded, sorted
	pkgs   map[string]*types.Package
	errs   map[string]string
	stamps map[string]time.Time // modification times of the loaded workspace files
	files  map[string][]string  // the Go files of each loaded package
	sigs   map[string]map[string]*setSig
}

func NewImporter() *Importer { return &Importer{pkgs: map[string]*types.Package{}} }

// load makes sure paths are loaded. Packages are loaded together so that
// they share their dependencies: one types.Package per import path.
func (im *Importer) load(dir string, paths []string) error {
	im.mu.Lock()
	defer im.mu.Unlock()
	missing := im.stale()
	for _, p := range paths {
		if _, ok := im.pkgs[p]; !ok {
			if _, failed := im.errs[p]; !failed {
				missing = true
			}
		}
	}
	if !missing {
		return nil
	}
	all := slices.Clone(im.paths)
	for _, p := range paths {
		if !slices.Contains(all, p) {
			all = append(all, p)
		}
	}
	sort.Strings(all)
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedFiles | packages.NeedTypes | packages.NeedImports | packages.NeedDeps, Dir: dir}
	loaded, err := packages.Load(cfg, all...)
	if err != nil {
		return fmt.Errorf("loading imports: %v", err)
	}
	im.paths = all
	im.pkgs = map[string]*types.Package{}
	im.errs = map[string]string{}
	im.stamps = map[string]time.Time{}
	im.files = map[string][]string{}
	im.sigs = map[string]map[string]*setSig{}
	goroot, modcache := filepath.Join(runtime.GOROOT(), "src"), os.Getenv("GOMODCACHE")
	if modcache == "" {
		modcache = filepath.Join(build.Default.GOPATH, "pkg", "mod")
	}
	packages.Visit(loaded, nil, func(p *packages.Package) {
		im.files[p.PkgPath] = p.GoFiles
		// Workspace packages can change while the editor runs.
		for _, f := range p.GoFiles {
			if strings.HasPrefix(f, goroot) || strings.HasPrefix(f, modcache) {
				break
			}
			if fi, err := os.Stat(f); err == nil {
				im.stamps[f] = fi.ModTime()
			}
		}
		if p.Types != nil && !p.IllTyped {
			im.pkgs[p.PkgPath] = p.Types
		}
		if len(p.Errors) > 0 && slices.Contains(all, p.PkgPath) {
			im.errs[p.PkgPath] = p.Errors[0].Msg
		}
	})
	return nil
}

// Preload loads, at once, what the packages in dirs import and what their
// generated code may: loading paths one package at a time would load every
// dependency again for each package that adds one. Directories in other
// modules than the first are left to load on their own.
func (im *Importer) Preload(dirs []string) error {
	if len(dirs) == 0 {
		return nil
	}
	root := moduleRoot(dirs[0])
	set := map[string]bool{}
	for _, d := range dirs {
		if moduleRoot(d) != root {
			continue
		}
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".ego") && !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			f, _ := parser.ParseFile(token.NewFileSet(), filepath.Join(d, e.Name()), nil, parser.ImportsOnly)
			if f == nil {
				continue
			}
			for _, s := range f.Imports {
				set[strings.Trim(s.Path.Value, `"`)] = true
			}
		}
	}
	for _, path := range lowered {
		set[path] = true
	}
	delete(set, "C")
	delete(set, "unsafe")
	return im.load(dirs[0], slices.Sorted(maps.Keys(set)))
}

// moduleRoot returns the directory of the go.mod that dir belongs to, or "".
func moduleRoot(dir string) string {
	d, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
		d = filepath.Dir(d)
	}
}

// Forget drops the package in dir, so the next load reads it again: after
// its generated Go was written, say, when it failed to load without it.
func (im *Importer) Forget(dir string) {
	path, err := ImportPath(dir)
	if err != nil {
		return
	}
	im.mu.Lock()
	defer im.mu.Unlock()
	delete(im.pkgs, path)
	delete(im.errs, path)
}

// dependents returns the loaded packages reachable from roots that import
// target, directly or not, each after the ones it imports.
func (im *Importer) dependents(target string, roots []string) []string {
	im.mu.Lock()
	defer im.mu.Unlock()
	imports := map[string]bool{} // whether a package imports target
	var order []string
	var visit func(path string) bool
	visit = func(path string) bool {
		if path == target {
			return true
		}
		if v, ok := imports[path]; ok {
			return v
		}
		imports[path] = false // also ends import cycles
		pkg := im.pkgs[path]
		if pkg == nil {
			return false
		}
		found := false
		for _, dep := range pkg.Imports() {
			if visit(dep.Path()) {
				found = true
			}
		}
		imports[path] = found
		if found {
			order = append(order, path)
		}
		return found
	}
	for _, r := range roots {
		visit(r)
	}
	return order
}

// goFiles returns the Go files of a loaded package.
func (im *Importer) goFiles(path string) []string {
	im.mu.Lock()
	defer im.mu.Unlock()
	return im.files[path]
}

// stale reports whether a loaded workspace file changed since it was loaded.
func (im *Importer) stale() bool {
	for f, t := range im.stamps {
		if fi, err := os.Stat(f); err != nil || !fi.ModTime().Equal(t) {
			return true
		}
	}
	return false
}

func (im *Importer) Import(path string) (*types.Package, error) {
	im.mu.Lock()
	defer im.mu.Unlock()
	if p, ok := im.pkgs[path]; ok {
		return p, nil
	}
	if msg, ok := im.errs[path]; ok {
		return nil, fmt.Errorf("%s", msg)
	}
	return nil, fmt.Errorf("package %s not loaded", path)
}

// setSigs returns the functions and methods that an imported package's
// .ego files declare with an error set in their signature.
func (im *Importer) setSigs(path string) map[string]*setSig {
	im.mu.Lock()
	defer im.mu.Unlock()
	if s, ok := im.sigs[path]; ok {
		return s
	}
	var files []*ast.File
	sums := map[string]*ast.SumDecl{}
	for _, f := range im.files[path] {
		base, ok := strings.CutSuffix(f, "_ego.go")
		if !ok {
			continue
		}
		src, err := os.ReadFile(base + ".ego")
		if err != nil {
			continue
		}
		af, err := parser.ParseFile(token.NewFileSet(), base+".ego", src, 0)
		if err != nil {
			continue
		}
		files = append(files, af)
		for _, d := range af.Decls {
			if sd, ok := d.(*ast.SumDecl); ok {
				sums[sd.Name.Name] = sd
			}
		}
	}
	s := setSigs(files, sums)
	if im.sigs != nil {
		im.sigs[path] = s
	}
	return s
}

// typeInfo is the result of type-checking one draft of the package.
type typeInfo struct {
	fset  *token.FileSet
	pkg   *types.Package
	info  *types.Info
	errs  []error
	files map[*fileGen]*fileTypes
}

// fileTypes finds the go/ast node a .ego node was rendered as.
type fileTypes struct {
	f     *goast.File
	tf    *token.File
	index map[[2]int]goast.Expr
	m     *SourceMap
	recs  map[ast.Node][2]int
}

func newFileTypes(fset *token.FileSet, f *goast.File, w *writer) *fileTypes {
	ft := &fileTypes{f: f, tf: fset.File(f.Pos()), index: map[[2]int]goast.Expr{}, m: newSourceMap(w), recs: map[ast.Node][2]int{}}
	for _, r := range w.recs {
		if _, ok := ft.recs[r.node]; !ok {
			ft.recs[r.node] = [2]int{r.start, r.end}
		}
	}
	goast.Inspect(f, func(n goast.Node) bool {
		if x, ok := n.(goast.Expr); ok && n.Pos().IsValid() && n.End().IsValid() {
			key := [2]int{ft.tf.Offset(n.Pos()), ft.tf.Offset(n.End())}
			if _, ok := ft.index[key]; !ok {
				ft.index[key] = x
			}
		}
		return true
	})
	return ft
}

// expr returns the go/ast expression n was rendered as in the draft.
func (g *fileGen) goExpr(n ast.Node) goast.Expr {
	ti := g.r.ti
	if ti == nil || n == nil {
		return nil
	}
	ft := ti.files[g]
	if ft == nil {
		return nil
	}
	span, ok := ft.recs[n]
	if !ok {
		a, b, ok2 := ft.m.span(g.off(n.Pos()), g.off(n.End()))
		if !ok2 {
			return nil
		}
		span = [2]int{a, b}
	}
	return ft.index[span]
}

// tv returns the type and value of a .ego expression, from the last draft.
func (g *fileGen) tv(n ast.Node) (types.TypeAndValue, bool) {
	x := g.goExpr(n)
	if x == nil {
		return types.TypeAndValue{}, false
	}
	if tv, ok := g.r.ti.info.Types[x]; ok && tv.Type != nil {
		return tv, true
	}
	if id, ok := x.(*goast.Ident); ok {
		if obj := g.r.ti.info.ObjectOf(id); obj != nil {
			return types.TypeAndValue{Type: obj.Type()}, true
		}
	}
	return types.TypeAndValue{}, false
}

// typeOf returns the type of a .ego expression, or nil.
func (g *fileGen) typeOf(n ast.Node) types.Type {
	tv, ok := g.tv(n)
	if !ok {
		return nil
	}
	if b, ok := tv.Type.(*types.Basic); ok && b.Kind() == types.Invalid {
		return nil
	}
	return tv.Type
}

// objectOf returns the object an identifier or selector refers to.
func (g *fileGen) objectOf(n ast.Expr) types.Object {
	x := g.goExpr(n)
	switch x := x.(type) {
	case *goast.Ident:
		return g.r.ti.info.ObjectOf(x)
	case *goast.SelectorExpr:
		return g.r.ti.info.ObjectOf(x.Sel)
	}
	return nil
}

// typeString renders t as Go source in this file, adding imports as needed.
func (g *fileGen) typeString(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string {
		if p.Path() == g.pkg.path {
			return ""
		}
		return g.use(p.Path(), p.Name())
	})
}

// zero returns the zero value of t as Go source.
func (g *fileGen) zero(t types.Type) string { return typeutil.Zero(t, g.typeString) }

var (
	isError         = typeutil.IsError
	implementsError = typeutil.ImplementsError
	isContext       = typeutil.IsContext
)

// results returns the result types of a call, or nil if unknown.
func results(t types.Type) []types.Type {
	switch t := t.(type) {
	case nil:
		return nil
	case *types.Tuple:
		var out []types.Type
		for v := range t.Variables() {
			out = append(out, v.Type())
		}
		return out
	}
	return []types.Type{t}
}

// verb returns the fmt verb that prints a value of type t naturally.
func verb(t types.Type) string {
	if t == nil {
		return "%v"
	}
	if types.Implements(t, stringer) || implementsError(t) {
		return "%v"
	}
	if b, ok := t.Underlying().(*types.Basic); ok {
		switch {
		case b.Info()&types.IsString != 0:
			return "%s"
		case b.Info()&types.IsInteger != 0:
			return "%d"
		}
	}
	return "%v"
}

var stringer = func() *types.Interface {
	sig := types.NewSignatureType(nil, nil, nil, nil, types.NewTuple(types.NewVar(token.NoPos, nil, "", types.Typ[types.String])), false)
	return types.NewInterfaceType([]*types.Func{types.NewFunc(token.NoPos, nil, "String", sig)}, nil).Complete()
}()

// sumCases returns the cases of a sum type (an error set or an enum with
// data): the named types in its package with the marker method is<Name>.
// It returns nil if t isn't one.
func sumCases(t types.Type) (set *types.Named, cases []*types.TypeName) {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return nil, nil
	}
	iface, ok := n.Underlying().(*types.Interface)
	if !ok || n.Obj().Pkg() == nil {
		return nil, nil
	}
	marker := "is" + n.Obj().Name()
	found := false
	for m := range iface.Methods() {
		if m.Name() == marker {
			found = true
		}
	}
	if !found {
		return nil, nil
	}
	scope := n.Obj().Pkg().Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() || tn == n.Obj() {
			continue
		}
		if _, isIface := tn.Type().Underlying().(*types.Interface); isIface {
			continue
		}
		if types.Implements(tn.Type(), iface) {
			cases = append(cases, tn)
		}
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].Pos() < cases[j].Pos() })
	return n, cases
}

// setOf returns the sum type a case type belongs to, from its marker method.
// isSet reports whether t is a named interface with the marker method.
func isSet(t types.Type, marker string) bool {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return false
	}
	iface, ok := n.Underlying().(*types.Interface)
	if !ok {
		return false
	}
	for m := range iface.Methods() {
		if m.Name() == marker {
			return true
		}
	}
	return false
}

func setOf(t types.Type) *types.Named {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return nil
	}
	ms := types.NewMethodSet(n)
	for sel := range ms.Methods() {
		name := sel.Obj().Name()
		if rest, ok := strings.CutPrefix(name, "is"); ok && rest != "" {
			// The set is the interface named rest with this marker method;
			// listing its cases (sumCases) would scan the whole package.
			if tn, ok := n.Obj().Pkg().Scope().Lookup(rest).(*types.TypeName); ok && isSet(tn.Type(), name) {
				return tn.Type().(*types.Named)
			}
		}
	}
	return nil
}

// enumConsts returns the constants of a named non-interface type declared in
// its package: Go's usual enum pattern.
func enumConsts(t types.Type) []*types.Const {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return nil
	}
	if _, ok := n.Underlying().(*types.Basic); !ok {
		return nil
	}
	var out []*types.Const
	scope := n.Obj().Pkg().Scope()
	for _, name := range scope.Names() {
		if c, ok := scope.Lookup(name).(*types.Const); ok && types.Identical(c.Type(), n) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pos() < out[j].Pos() })
	return out
}
