package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/effect-go/effect-go/internal/layers"
	"github.com/effect-go/effect-go/internal/lower"
)

func generate(args []string) error {
	fl := flag.NewFlagSet("generate", flag.ExitOnError)
	lines := fl.Bool("lines", true, "add //line directives pointing at the .ego files")
	check := fl.Bool("check", false, "write nothing; fail if a generated file is missing or stale (for CI)")
	fl.Parse(args)
	checkOnly = *check
	dirs, err := egoDirs(fl.Args())
	if err != nil {
		return err
	}
	im := lower.NewImporter()
	failed := false
	for _, dir := range dirs {
		res, err := lower.Generate(lower.Config{Dir: dir, NoLines: !*lines, Importer: im})
		if err != nil {
			return err
		}
		for _, d := range res.Diags {
			fmt.Fprintln(os.Stderr, rel(d.Pos.String())+": "+d.Msg)
		}
		if len(res.Diags) > 0 {
			if os.Getenv("EGO_DEBUG") != "" {
				for _, out := range res.Outputs {
					fmt.Fprintf(os.Stderr, "--- %s\n%s\n", out.Go, out.Raw)
				}
			}
			failed = true
			continue
		}
		for _, out := range res.Outputs {
			if err := write(out.Go, out.Code); err != nil {
				return err
			}
		}
		// Layers are wired after the .ego files compile: injectors may use
		// their declarations.
		lr, err := layers.Generate(dir)
		if err != nil {
			return err
		}
		if lr != nil {
			for _, d := range lr.Diags {
				fmt.Fprintln(os.Stderr, rel(d.Pos.String())+": "+d.Msg)
			}
			if len(lr.Diags) > 0 {
				failed = true
				continue
			}
			if err := write(lr.Path, lr.Code); err != nil {
				return err
			}
		}
	}
	if failed || stale {
		return errSilent
	}
	return nil
}

var stale bool

var checkOnly bool

// write writes a generated file if it changed, and prints its name. With
// -check it only reports the file as stale.
func write(path string, code []byte) error {
	if old, _ := os.ReadFile(path); bytes.Equal(old, code) {
		return nil
	}
	if checkOnly {
		fmt.Fprintln(os.Stderr, rel(path)+": stale; run ego generate")
		stale = true
		return nil
	}
	if err := os.WriteFile(path, code, 0o666); err != nil {
		return err
	}
	fmt.Println(rel(path))
	return nil
}

func rel(path string) string {
	wd, _ := os.Getwd()
	if r, err := filepath.Rel(wd, path); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return path
}

// egoDirs returns the directories with .ego files named by args: a
// directory, or dir/... for a tree.
func egoDirs(args []string) ([]string, error) {
	if len(args) == 0 {
		args = []string{"./..."}
	}
	seen := map[string]bool{}
	var dirs []string
	add := func(d string) {
		if !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	for _, a := range args {
		if root, ok := strings.CutSuffix(a, "/..."); ok {
			err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() && path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata" || d.Name() == "vendor") {
					return filepath.SkipDir
				}
				if !d.IsDir() && (strings.HasSuffix(path, ".ego") || strings.HasSuffix(path, ".go") && layers.HasInjectors(filepath.Dir(path))) {
					add(filepath.Dir(path))
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasSuffix(a, ".ego") {
			add(filepath.Dir(a))
			continue
		}
		add(a)
	}
	return dirs, nil
}
