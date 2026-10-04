// Package lower compiles effect-go (.ego) files to Go.
//
// Each .ego file becomes a _ego.go file next to it. Code that uses no dialect
// syntax is copied byte for byte; each dialect construct is replaced by the
// Go you would write by hand. Constructs that need types (implicit ctx, the
// closures behind all and race, zero values, lambdas) get them from go/types:
// the package is rendered as a draft, type-checked, and rendered again until
// the draft stops changing.
//
// Debugging: EGO_DEBUG=1 makes ego generate print the generated code of a
// package that fails to compile; EGO_DEBUG=2 also prints every draft and its
// type errors. EGO_LSP_TRACE=1 with ego lsp -log=file logs every LSP message.
package lower

import (
	"bytes"
	"errors"
	"fmt"
	goast "go/ast"
	"go/build"
	"go/build/constraint"
	"go/format"
	goparser "go/parser"
	"go/token"
	"go/types"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/effect-go/effect-go/internal/syntax/ast"
	"github.com/effect-go/effect-go/internal/syntax/parser"
	"github.com/effect-go/effect-go/internal/syntax/scanner"
)

// Runtime import paths used by generated code.
const (
	modPath      = "github.com/effect-go/effect-go"
	scopePath    = modPath + "/scope"
	schedulePath = modPath + "/schedule"
	tracePath    = modPath + "/trace"
)

// Config says what to generate.
type Config struct {
	Dir string // package directory
	// Overlay holds file contents that replace the files on disk, by
	// absolute path. The editor proxy uses it for unsaved .ego files.
	Overlay map[string][]byte
	// NoLines turns off //line directives in the output.
	NoLines bool
	// NoTypeCheck skips type-checking the generated code. The editor proxy
	// sets it: gopls reports those errors itself.
	NoTypeCheck bool
	// Importer, if set, is reused across calls to cache loaded packages.
	Importer *Importer
}

// An Output is the Go generated from one .ego file.
type Output struct {
	Ego  string     // path of the .ego file
	Go   string     // path of the generated file
	Src  []byte     // the .ego source
	Code []byte     // the generated Go, formatted, with //line directives
	Raw  []byte     // the generated Go before formatting; Map describes it
	Map  *SourceMap // between Src and Raw
	File *token.File
}

// A Diagnostic is an error in a .ego or .go file.
type Diagnostic struct {
	Pos token.Position
	End token.Position // may be invalid
	Msg string
}

func (d Diagnostic) String() string { return fmt.Sprintf("%v: %s", d.Pos, d.Msg) }

// Result is what Generate produced.
type Result struct {
	Outputs []*Output
	Diags   []Diagnostic
	Fset    *token.FileSet
}

// Err returns the diagnostics as one error, or nil.
func (r *Result) Err() error {
	if len(r.Diags) == 0 {
		return nil
	}
	var b strings.Builder
	for i, d := range r.Diags {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(d.String())
	}
	return errors.New(b.String())
}

// GoName returns the name of the file generated from a .ego file.
func GoName(ego string) string {
	base := strings.TrimSuffix(ego, ".ego")
	if t, ok := strings.CutSuffix(base, "_test"); ok {
		return t + "_ego_test.go"
	}
	return base + "_ego.go"
}

// Generate compiles the .ego files in cfg.Dir. A Result is returned even
// when there are diagnostics; err is for failures to run at all.
func Generate(cfg Config) (*Result, error) {
	dir, err := filepath.Abs(cfg.Dir)
	if err != nil {
		return nil, err
	}
	cfg.Dir = dir
	if cfg.Importer == nil {
		cfg.Importer = NewImporter()
	}
	p := &pkgGen{cfg: cfg, fset: token.NewFileSet(), facts: map[ast.Node]*fact{}}
	res := &Result{Fset: p.fset}
	if err := p.parse(res); err != nil {
		return nil, err
	}
	if len(p.files)+len(p.testFiles)+len(p.xtestFiles) == 0 || len(res.Diags) > 0 {
		return res, nil // nothing to do, or syntax errors
	}
	// Load what the package and its tests import at once. Loading more paths
	// later loads their dependencies again, so the external tests would see
	// another net/http than the package under test does.
	paths, err := p.imports()
	if err != nil {
		return nil, err
	}
	if err := cfg.Importer.load(cfg.Dir, paths); err != nil {
		return nil, err
	}

	// The package itself.
	ti, err := p.run(res, nil)
	if err != nil {
		return nil, err
	}

	// In-package tests are checked with the package, as go test does.
	under := p
	if len(p.testFiles) > 0 {
		under = p.variant(append(slices.Clone(p.files), p.testFiles...), append(slices.Clone(p.goFiles), p.testGo...), p.path, p.name)
		ti, err = under.run(res, p.testFiles)
		if err != nil {
			return nil, err
		}
	}

	// External tests are their own package, importing the one under test
	// (with its in-package test files, again as go test does).
	if len(p.xtestFiles) > 0 {
		v := p.variant(p.xtestFiles, p.xtestGo, p.path+"_test", p.name+"_test")
		if ti != nil && ti.pkg != nil {
			v.override = map[string]*types.Package{p.path: ti.pkg}
			v.overrideSigs = map[string]map[string]*setSig{p.path: under.sigs}
		}
		if _, err := v.run(res, p.xtestFiles); err != nil {
			return nil, err
		}
	}
	sortDiags(res.Diags)
	return res, nil
}

// lowered are the packages generated code may import that its .ego file
// doesn't.
var lowered = []string{"context", "errors", "fmt", "strconv", "time", scopePath, schedulePath, tracePath}

// imports returns every package the package and its tests import, and
// those that lowering may add, except the package itself.
func (p *pkgGen) imports() ([]string, error) {
	set := map[string]bool{}
	for _, f := range slices.Concat(p.files, p.testFiles, p.xtestFiles) {
		for _, s := range f.file.Imports {
			set[strings.Trim(s.Path.Value, `"`)] = true
		}
	}
	for _, path := range slices.Concat(p.goFiles, p.testGo, p.xtestGo) {
		src, err := p.read(path)
		if err != nil {
			return nil, err
		}
		gf, err := goparser.ParseFile(token.NewFileSet(), path, src, goparser.ImportsOnly)
		if gf == nil {
			return nil, err
		}
		for _, s := range gf.Imports {
			set[strings.Trim(s.Path.Value, `"`)] = true
		}
	}
	for _, path := range lowered {
		set[path] = true
	}
	delete(set, p.path)
	delete(set, "C")
	delete(set, "unsafe")
	return slices.Sorted(maps.Keys(set)), nil
}

// variant returns a pkgGen for another set of files: the package with its
// tests, or the external test package.
func (p *pkgGen) variant(files []*fileGen, goFiles []string, path, name string) *pkgGen {
	v := &pkgGen{cfg: p.cfg, fset: p.fset, name: name, path: path, goFiles: goFiles, facts: map[ast.Node]*fact{}}
	for _, f := range files {
		nf := *f
		nf.pkg = v
		v.files = append(v.files, &nf)
	}
	return v
}

// run compiles p's .ego files, adding to res the outputs and diagnostics of
// the files in only (all of p's files if only is nil). It returns the
// package's types.
func (p *pkgGen) run(res *Result, only []*fileGen) (*typeInfo, error) {
	keep := func(f *fileGen) bool {
		if only == nil {
			return true
		}
		for _, o := range only {
			if o.path == f.path {
				return true
			}
		}
		return false
	}
	cfg := p.cfg
	p.collect()

	// Draft rounds, until the drafts stop changing.
	var ti *typeInfo
	var prev [][]byte
	var err error
	for i := 0; i < 6; i++ {
		var drafts [][]byte
		var ws []*writer
		for _, f := range p.files {
			w := f.render(&round{ti: ti})
			ws = append(ws, w)
			drafts = append(drafts, w.buf)
		}
		if prev != nil && slices.EqualFunc(prev, drafts, bytes.Equal) {
			break
		}
		prev = drafts
		ti, err = p.check(ws, true)
		if err != nil {
			return nil, err
		}
		if os.Getenv("EGO_DEBUG") == "2" {
			for _, d := range drafts {
				fmt.Fprintf(os.Stderr, "--- draft %d\n%s\n", i, d)
			}
			for _, e := range ti.errs {
				fmt.Fprintln(os.Stderr, "draft error:", e)
			}
		}
		if len(p.files) == 0 {
			break // plain Go: one check gives the types
		}
	}

	// Final rendering.
	var ws []*writer
	var outs []*Output
	var diags []Diagnostic
	for _, f := range p.files {
		w := f.render(&round{ti: ti, final: true})
		ws = append(ws, w)
		if !keep(f) {
			continue
		}
		diags = append(diags, f.diags...)
		outs = append(outs, &Output{Ego: f.path, Go: filepath.Join(cfg.Dir, GoName(f.name)), Src: f.src, Raw: w.buf, Map: newSourceMap(w), File: f.tf})
	}
	if len(diags) == 0 && !cfg.NoTypeCheck {
		// Type-check the result, and report its errors at .ego positions.
		final, err := p.check(ws, false)
		if err != nil {
			return nil, err
		}
		diags = append(diags, p.mapErrors(final, outs)...)
		ti = final
	}
	for _, out := range outs {
		code := out.Raw
		if !cfg.NoLines {
			code = addLineDirectives(code, out)
		}
		if formatted, err := format.Source(code); err == nil {
			code = formatted
		} else if len(diags) == 0 {
			diags = append(diags, Diagnostic{Pos: token.Position{Filename: out.Ego}, Msg: "internal error: generated code doesn't parse: " + err.Error()})
		}
		out.Code = code
	}
	res.Outputs = append(res.Outputs, outs...)
	res.Diags = append(res.Diags, diags...)
	return ti, nil
}

func sortDiags(ds []Diagnostic) {
	sort.SliceStable(ds, func(i, j int) bool {
		a, b := ds[i].Pos, ds[j].Pos
		if a.Filename != b.Filename {
			return a.Filename < b.Filename
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	// Drop exact duplicates.
	out := ds[:0]
	for i, d := range ds {
		if i > 0 && d.Pos == ds[i-1].Pos && d.Msg == ds[i-1].Msg {
			continue
		}
		out = append(out, d)
	}
	clear(ds[len(out):])
}

// read returns a file's contents, from the overlay if it's there.
func (p *pkgGen) read(path string) ([]byte, error) {
	if b, ok := p.cfg.Overlay[path]; ok {
		return b, nil
	}
	return os.ReadFile(path)
}

// parse parses the package's .ego files and lists its .go files.
func (p *pkgGen) parse(res *Result) error {
	entries, err := os.ReadDir(p.cfg.Dir)
	if err != nil {
		return err
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	for path := range p.cfg.Overlay {
		if filepath.Dir(path) == p.cfg.Dir {
			names[filepath.Base(path)] = true
		}
	}
	var egos, gos []string
	for name := range names {
		switch {
		case strings.HasSuffix(name, ".ego"):
			egos = append(egos, name)
		case strings.HasSuffix(name, ".go"):
			gos = append(gos, name)
		}
	}
	sort.Strings(egos)
	sort.Strings(gos)
	generated := map[string]bool{}
	var tests []*fileGen
	for _, name := range egos {
		generated[GoName(name)] = true
		path := filepath.Join(p.cfg.Dir, name)
		src, err := p.read(path)
		if err != nil {
			return err
		}
		if !matchBuild(src) {
			continue
		}
		f, err := parser.ParseFile(p.fset, path, src, parser.ParseComments)
		if err != nil {
			var list scanner.ErrorList
			if errors.As(err, &list) {
				for _, e := range list {
					res.Diags = append(res.Diags, Diagnostic{Pos: e.Pos, Msg: e.Msg})
				}
				continue
			}
			return err
		}
		fg := &fileGen{pkg: p, name: name, path: path, src: src, file: f, tf: p.fset.File(f.Pos())}
		if strings.HasSuffix(name, "_test.ego") {
			tests = append(tests, fg)
			continue
		}
		p.files = append(p.files, fg)
		if p.name == "" {
			p.name = f.Name.Name
		}
	}
	// The files are read as the layers generator reads them: with the
	// injector files, which declare the Build functions, and without the
	// layers_ego.go they become. A new project has no layers_ego.go yet, and
	// an old one may be stale.
	bctx := build.Default
	bctx.BuildTags = append(slices.Clip(bctx.BuildTags), "egolayers")
	var testGos []string
	for _, name := range gos {
		if generated[name] {
			continue
		}
		if ok, err := bctx.MatchFile(p.cfg.Dir, name); err != nil || !ok {
			continue
		}
		path := filepath.Join(p.cfg.Dir, name)
		if strings.HasSuffix(name, "_test.go") {
			testGos = append(testGos, path)
			continue
		}
		p.goFiles = append(p.goFiles, path)
		if p.name == "" {
			if src, err := p.read(path); err == nil {
				if gf, err := goparser.ParseFile(token.NewFileSet(), path, src, goparser.PackageClauseOnly); err == nil {
					p.name = gf.Name.Name
				}
			}
		}
	}
	// Test files in package x_test are the external test package.
	for _, f := range tests {
		if p.name == "" {
			p.name = strings.TrimSuffix(f.file.Name.Name, "_test")
		}
		if f.file.Name.Name == p.name+"_test" {
			p.xtestFiles = append(p.xtestFiles, f)
		} else {
			p.testFiles = append(p.testFiles, f)
		}
	}
	for _, path := range testGos {
		src, err := p.read(path)
		if err != nil {
			continue
		}
		gf, err := goparser.ParseFile(token.NewFileSet(), path, src, goparser.PackageClauseOnly)
		if err != nil {
			continue
		}
		if gf.Name.Name == p.name+"_test" {
			p.xtestGo = append(p.xtestGo, path)
		} else {
			p.testGo = append(p.testGo, path)
		}
	}
	path, err := ImportPath(p.cfg.Dir)
	if err != nil {
		return err
	}
	p.path = path
	return nil
}

// ImportPath returns the import path of the package in dir, from go.mod.
func ImportPath(dir string) (string, error) {
	for d := dir; ; d = filepath.Dir(d) {
		data, err := os.ReadFile(filepath.Join(d, "go.mod"))
		if err == nil {
			for line := range strings.SplitSeq(string(data), "\n") {
				if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
					mod := strings.Trim(strings.TrimSpace(rest), `"`)
					rel, _ := filepath.Rel(d, dir)
					if rel == "." {
						return mod, nil
					}
					return mod + "/" + filepath.ToSlash(rel), nil
				}
			}
			return "", fmt.Errorf("%s/go.mod has no module line", d)
		}
		if filepath.Dir(d) == d {
			return "", fmt.Errorf("%s is not in a Go module", dir)
		}
	}
}

// check type-checks the rendered files, plus the package's .go files and,
// for drafts, the draft helpers.
func (p *pkgGen) check(ws []*writer, draft bool) (*typeInfo, error) {
	fset := token.NewFileSet()
	ti := &typeInfo{fset: fset, files: map[*fileGen]*fileTypes{}}
	var files []*goast.File
	imports := map[string]bool{}
	for i, f := range p.files {
		name := filepath.Join(p.cfg.Dir, GoName(f.name))
		gf, _ := goparser.ParseFile(fset, name, ws[i].buf, goparser.SkipObjectResolution|goparser.AllErrors)
		if gf == nil {
			continue
		}
		files = append(files, gf)
		ti.files[f] = newFileTypes(fset, gf, ws[i])
		for _, s := range gf.Imports {
			imports[strings.Trim(s.Path.Value, `"`)] = true
		}
	}
	for _, path := range p.goFiles {
		src, err := p.read(path)
		if err != nil {
			return nil, err
		}
		gf, err := goparser.ParseFile(fset, path, src, goparser.SkipObjectResolution|goparser.ParseComments)
		if gf == nil {
			return nil, err
		}
		files = append(files, gf)
		for _, s := range gf.Imports {
			imports[strings.Trim(s.Path.Value, `"`)] = true
		}
	}
	if draft {
		gf, err := goparser.ParseFile(fset, filepath.Join(p.cfg.Dir, "_ego_draft.go"), prelude(p.name), 0)
		if err != nil {
			return nil, fmt.Errorf("draft helpers: %v", err)
		}
		files = append(files, gf)
		imports["time"] = true
		imports[schedulePath] = true
	}
	var paths []string
	for path := range imports {
		if path != "C" && path != "unsafe" {
			paths = append(paths, path)
		}
	}
	paths = slices.DeleteFunc(paths, func(path string) bool { return p.override[path] != nil })
	if err := p.cfg.Importer.load(p.cfg.Dir, paths); err != nil {
		return nil, err
	}
	var imp types.Importer = p.cfg.Importer
	if p.override != nil {
		imp = overrideImporter{p.override, p.cfg.Importer}
	}
	conf := types.Config{
		Importer:    imp,
		FakeImportC: true,
		Error:       func(err error) { ti.errs = append(ti.errs, err) },
	}
	ti.info = &types.Info{
		Types:      map[goast.Expr]types.TypeAndValue{},
		Defs:       map[*goast.Ident]types.Object{},
		Uses:       map[*goast.Ident]types.Object{},
		Scopes:     map[goast.Node]*types.Scope{},
		Selections: map[*goast.SelectorExpr]*types.Selection{},
	}
	ti.pkg, _ = conf.Check(p.path, fset, files, ti.info)
	return ti, nil
}

// mapErrors returns the type errors of the final code, at .ego positions.
func (p *pkgGen) mapErrors(ti *typeInfo, outs []*Output) []Diagnostic {
	byName := map[string]*Output{}
	for _, o := range outs {
		byName[o.Go] = o
	}
	var ds []Diagnostic
	for _, err := range ti.errs {
		var te types.Error
		if !errors.As(err, &te) {
			ds = append(ds, Diagnostic{Msg: err.Error()})
			continue
		}
		// Errors in .go files are the Go compiler's to report: they may
		// only be waiting for code ego generates next, such as layers.
		pos := ti.fset.Position(te.Pos)
		if o, ok := byName[pos.Filename]; ok {
			src, _ := o.Map.ToSource(pos.Offset)
			ds = append(ds, Diagnostic{Pos: o.File.Position(o.File.Pos(min(src, o.File.Size()))), Msg: te.Msg})
		}
	}
	return ds
}

// matchBuild reports whether a .ego file's //go:build line, if any, is
// satisfied by the default build context.
func matchBuild(src []byte) bool {
	for line := range strings.SplitSeq(string(src), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") && !constraint.IsGoBuild(line) {
			continue
		}
		if !constraint.IsGoBuild(line) {
			return true // past the header
		}
		x, err := constraint.Parse(line)
		if err != nil {
			return true
		}
		ctx := build.Default
		return x.Eval(func(tag string) bool {
			if tag == ctx.GOOS || tag == ctx.GOARCH || tag == ctx.Compiler || tag == "cgo" && ctx.CgoEnabled {
				return true
			}
			if tag == "unix" {
				return ctx.GOOS != "windows" && ctx.GOOS != "plan9" && ctx.GOOS != "js" && ctx.GOOS != "wasip1"
			}
			return slices.Contains(ctx.ReleaseTags, tag) || slices.Contains(ctx.BuildTags, tag) || slices.Contains(ctx.ToolTags, tag)
		})
	}
	return true
}

// overrideImporter imports some packages from types checked here: the
// package under test, for its external tests.
type overrideImporter struct {
	pkgs map[string]*types.Package
	next types.Importer
}

func (o overrideImporter) Import(path string) (*types.Package, error) {
	if p, ok := o.pkgs[path]; ok {
		return p, nil
	}
	return o.next.Import(path)
}
