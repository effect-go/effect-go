package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/effect-go/effect-go/internal/lower"
)

func generate(args []string) error {
	fl := flag.NewFlagSet("generate", flag.ExitOnError)
	lines := fl.Bool("lines", true, "add //line directives pointing at the .ego files")
	fl.Parse(args)
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
			old, _ := os.ReadFile(out.Go)
			if bytes.Equal(old, out.Code) {
				continue
			}
			if err := os.WriteFile(out.Go, out.Code, 0o666); err != nil {
				return err
			}
			fmt.Println(rel(out.Go))
		}
	}
	if failed {
		return errSilent
	}
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
				if !d.IsDir() && strings.HasSuffix(path, ".ego") {
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
