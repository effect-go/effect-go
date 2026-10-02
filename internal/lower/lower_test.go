package lower

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the generated files in testdata")

// fixtures are the testdata packages that compile: their generated files are
// committed, and their tests run against the generated code.
func fixtures(t *testing.T) []string {
	dirs, _ := filepath.Glob("testdata/*")
	var out []string
	for _, d := range dirs {
		if egos, _ := filepath.Glob(filepath.Join(d, "*.ego")); len(egos) > 0 {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		t.Fatal("no fixtures")
	}
	return out
}

// Generating each fixture gives the committed Go.
func TestGolden(t *testing.T) {
	im := NewImporter()
	for _, dir := range fixtures(t) {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			res, err := Generate(Config{Dir: dir, Importer: im})
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range res.Diags {
				t.Error(d)
			}
			for _, out := range res.Outputs {
				if *update {
					if err := os.WriteFile(out.Go, out.Code, 0o666); err != nil {
						t.Fatal(err)
					}
					continue
				}
				want, err := os.ReadFile(out.Go)
				if err != nil {
					t.Fatalf("%v (run go test -update)", err)
				}
				if !bytes.Equal(out.Code, want) {
					t.Errorf("%s differs from the generated code (run go test -update):\n%s", out.Go, out.Code)
				}
			}
		})
	}
}

// The generated fixtures pass go vet and their own tests, with the race
// detector.
func TestFixturesRun(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go vet and go test")
	}
	var pkgs []string
	for _, d := range fixtures(t) {
		pkgs = append(pkgs, "./"+d)
	}
	for _, args := range [][]string{{"vet"}, {"test", "-race", "-count=1"}} {
		cmd := exec.Command("go", append(args, pkgs...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", args[0], err, out)
		}
	}
}

var errorRx = regexp.MustCompile(`// ERROR "([^"]*)"`)

// Each file in testdata/errors is a package whose lines marked
// // ERROR "regexp" must be reported, and nothing else.
func TestErrors(t *testing.T) {
	cases, _ := filepath.Glob("testdata/errors/*")
	im := NewImporter()
	for _, dir := range cases {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			res, err := Generate(Config{Dir: dir, Importer: im})
			if err != nil {
				t.Fatal(err)
			}
			want := map[int]*regexp.Regexp{}
			egos, _ := filepath.Glob(filepath.Join(dir, "*.ego"))
			for _, f := range egos {
				src, _ := os.ReadFile(f)
				for i, line := range strings.Split(string(src), "\n") {
					if m := errorRx.FindStringSubmatch(line); m != nil {
						want[i+1] = regexp.MustCompile(m[1])
					}
				}
			}
			got := map[int]bool{}
			for _, d := range res.Diags {
				rx, ok := want[d.Pos.Line]
				if !ok || !rx.MatchString(d.Msg) {
					t.Errorf("unexpected: %v", d)
					continue
				}
				got[d.Pos.Line] = true
			}
			for line, rx := range want {
				if !got[line] {
					t.Errorf("line %d: missing error matching %q", line, rx)
				}
			}
		})
	}
}
