// Command sync merges a new Go release's go/ast, go/scanner, go/parser,
// go/printer and go/format into the forks in internal/syntax, keeping the
// effect-go additions:
//
//	go run ./internal/syntax/sync go1.28.0
//
// For each file it merges three versions with git merge-file: the release
// the fork was made from (internal/syntax/UPSTREAM), the fork, and the new
// release. Pristine sources come from the local Go installation when its
// version matches, or else from the toolchain module on the Go proxy, as
// GOTOOLCHAIN downloads them. Conflicts are left in the files, marked as
// git marks them. Afterwards, go test ./internal/syntax/... checks that
// ego fmt still formats the new standard library exactly as gofmt does.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

const module = "github.com/effect-go/effect-go/internal/syntax/"

var pkgs = []string{"ast", "scanner", "parser", "printer", "format"}

func main() {
	if len(os.Args) != 2 || !strings.HasPrefix(os.Args[1], "go1.") {
		fmt.Fprintln(os.Stderr, "usage: go run ./internal/syntax/sync go1.N.M")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "sync:", err)
		os.Exit(1)
	}
}

func run(next string) error {
	root := "internal/syntax"
	b, err := os.ReadFile(filepath.Join(root, "UPSTREAM"))
	if err != nil {
		return fmt.Errorf("run from the repository root: %w", err)
	}
	base := strings.TrimSpace(string(b))
	baseRoot, err := goroot(base)
	if err != nil {
		return err
	}
	nextRoot, err := goroot(next)
	if err != nil {
		return err
	}
	conflicts := 0
	for _, pkg := range pkgs {
		names, err := goFiles(filepath.Join(nextRoot, "src/go", pkg))
		if err != nil {
			return err
		}
		ours, _ := goFiles(filepath.Join(root, pkg))
		for _, name := range ours {
			if !slices.Contains(names, name) && !strings.HasSuffix(name, "ego.go") {
				fmt.Printf("%s/%s: gone from %s; remove it if nothing needs it\n", pkg, name, next)
			}
		}
		for _, name := range names {
			theirs, err := upstream(nextRoot, pkg, name)
			if err != nil {
				return err
			}
			path := filepath.Join(root, pkg, name)
			mine, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				fmt.Printf("%s/%s: new in %s\n", pkg, name, next)
				if err := os.WriteFile(path, theirs, 0o666); err != nil {
					return err
				}
				continue
			} else if err != nil {
				return err
			}
			old, err := upstream(baseRoot, pkg, name)
			if errors.Is(err, os.ErrNotExist) {
				old = nil // added to the fork and to Go independently
			} else if err != nil {
				return err
			}
			merged, n, err := merge(mine, old, theirs)
			if err != nil {
				return err
			}
			if n > 0 {
				fmt.Printf("%s/%s: %d conflicts\n", pkg, name, n)
				conflicts += n
			}
			if !bytes.Equal(merged, mine) {
				if err := os.WriteFile(path, merged, 0o666); err != nil {
					return err
				}
			}
		}
	}
	if err := os.WriteFile(filepath.Join(root, "UPSTREAM"), []byte(next+"\n"), 0o666); err != nil {
		return err
	}
	fmt.Printf("merged %s into the fork of %s", next, base)
	if conflicts > 0 {
		fmt.Printf(", with %d conflicts to resolve", conflicts)
	}
	fmt.Println(". Then: update internal/syntax/README.md, and run go test ./internal/syntax/...")
	return nil
}

// upstream returns a file of go/<pkg> in a Go tree, with its imports of the
// forked packages pointing at the forks, as in internal/syntax.
func upstream(goroot, pkg, name string) ([]byte, error) {
	src, err := os.ReadFile(filepath.Join(goroot, "src/go", pkg, name))
	if err != nil {
		return nil, err
	}
	for _, p := range pkgs {
		src = bytes.ReplaceAll(src, []byte(`"go/`+p+`"`), []byte(`"`+module+p+`"`))
	}
	return src, nil
}

// merge merges the changes from base to theirs into ours, and returns the
// number of conflicts.
func merge(ours, base, theirs []byte) ([]byte, int, error) {
	dir, err := os.MkdirTemp("", "sync")
	if err != nil {
		return nil, 0, err
	}
	defer os.RemoveAll(dir)
	files := []string{filepath.Join(dir, "fork"), filepath.Join(dir, "base"), filepath.Join(dir, "go")}
	for i, b := range [][]byte{ours, base, theirs} {
		if err := os.WriteFile(files[i], b, 0o666); err != nil {
			return nil, 0, err
		}
	}
	out, err := exec.Command("git", append([]string{"merge-file", "-p"}, files...)...).Output()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return out, 0, nil
	case errors.As(err, &exit) && exit.ExitCode() > 0:
		return out, exit.ExitCode(), nil // the number of conflicts
	}
	return nil, 0, err
}

// goroot returns a Go tree for version: the local one if it matches,
// else the toolchain module from the Go proxy.
func goroot(version string) (string, error) {
	if runtime.Version() == version {
		return runtime.GOROOT(), nil
	}
	mod := fmt.Sprintf("golang.org/toolchain@v0.0.1-%s.%s-%s", version, runtime.GOOS, runtime.GOARCH)
	out, err := exec.Command("go", "mod", "download", "-json", mod).Output()
	if err != nil {
		return "", fmt.Errorf("download %s: %w", mod, err)
	}
	var m struct{ Dir string }
	if err := json.Unmarshal(out, &m); err != nil {
		return "", err
	}
	return m.Dir, nil
}

func goFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
