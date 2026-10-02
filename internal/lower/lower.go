// Package lower compiles effect-go (.ego) files to Go.
//
// Each .ego file becomes a _ego.go file next to it. Code that uses no dialect
// syntax is copied byte for byte; each dialect construct is replaced by the
// Go you would write by hand. Constructs that need types (implicit ctx, the
// closures behind all and race, zero values, lambdas) get them from go/types:
// the package is rendered as a draft, type-checked, and rendered again until
// the draft stops changing.
package lower

import (
	"bytes"
	"errors"
	"fmt"
	goast "go/ast"
	"go/build"
	"go/format"
	goparser "go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/effect-go/effect-go/internal/syntax/ast"
	"github.com/effect-go/effect-go/internal/syntax/parser"
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
	if len(p.files) == 0 {
		return res, nil
	}
	if len(res.Diags) > 0 { // syntax errors
		return res, nil
	}
	p.collect()

	// Draft rounds, until the drafts stop changing.
	var ti *typeInfo
	var prev [][]byte
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
	}

	// Final rendering.
	var ws []*writer
	for _, f := range p.files {
		w := f.render(&round{ti: ti, final: true})
		ws = append(ws, w)
		res.Diags = append(res.Diags, f.diags...)
		out := &Output{Ego: f.path, Go: filepath.Join(dir, GoName(f.name)), Src: f.src, Raw: w.buf, Map: newSourceMap(w), File: f.tf}
		res.Outputs = append(res.Outputs, out)
	}
	if len(res.Diags) == 0 && !cfg.NoTypeCheck {
		// Type-check the result, and report its errors at .ego positions.
		final, err := p.check(ws, false)
		if err != nil {
			return nil, err
		}
		res.Diags = append(res.Diags, p.mapErrors(final, res.Outputs)...)
	}
	for _, out := range res.Outputs {
		code := out.Raw
		if !cfg.NoLines {
			code = addLineDirectives(code, out)
		}
		if formatted, err := format.Source(code); err == nil {
			code = formatted
		} else if len(res.Diags) == 0 {
			res.Diags = append(res.Diags, Diagnostic{Pos: token.Position{Filename: out.Ego}, Msg: "internal error: generated code doesn't parse: " + err.Error()})
		}
		out.Code = code
	}
	sortDiags(res.Diags)
	return res, nil
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
		case strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go"):
			gos = append(gos, name)
		}
	}
	sort.Strings(egos)
	sort.Strings(gos)
	generated := map[string]bool{}
	for _, name := range egos {
		if strings.HasSuffix(name, "_test.ego") {
			res.Diags = append(res.Diags, Diagnostic{Pos: token.Position{Filename: filepath.Join(p.cfg.Dir, name)}, Msg: "test files can't be written in effect-go yet: use a _test.go file"})
			continue
		}
		generated[GoName(name)] = true
		path := filepath.Join(p.cfg.Dir, name)
		src, err := p.read(path)
		if err != nil {
			return err
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
		p.files = append(p.files, fg)
		if p.name == "" {
			p.name = f.Name.Name
		}
	}
	for _, name := range gos {
		if generated[name] {
			continue
		}
		if ok, err := build.Default.MatchFile(p.cfg.Dir, name); err != nil || !ok {
			continue
		}
		p.goFiles = append(p.goFiles, filepath.Join(p.cfg.Dir, name))
	}
	path, err := importPath(p.cfg.Dir)
	if err != nil {
		return err
	}
	p.path = path
	return nil
}

// importPath returns the import path of the package in dir, from go.mod.
func importPath(dir string) (string, error) {
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
	if err := p.cfg.Importer.load(p.cfg.Dir, paths); err != nil {
		return nil, err
	}
	conf := types.Config{
		Importer:    p.cfg.Importer,
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
		pos := ti.fset.Position(te.Pos)
		if o, ok := byName[pos.Filename]; ok {
			src, _ := o.Map.ToSource(pos.Offset)
			ds = append(ds, Diagnostic{Pos: o.File.Position(o.File.Pos(min(src, o.File.Size()))), Msg: te.Msg})
			continue
		}
		ds = append(ds, Diagnostic{Pos: pos, Msg: te.Msg})
	}
	return ds
}
