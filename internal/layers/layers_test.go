package layers

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// Each injector in testdata/bad reports the error marked on its line.
func TestErrors(t *testing.T) {
	res, err := Generate("testdata/bad")
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]*regexp.Regexp{}
	src, _ := os.ReadFile(filepath.Join("testdata/bad", "inject.go"))
	for i, line := range strings.Split(string(src), "\n") {
		if m := regexp.MustCompile(`// ERROR "(.*)"`).FindStringSubmatch(line); m != nil {
			want[i+1] = regexp.MustCompile(m[1])
		}
	}
	got := map[int]bool{}
	for _, d := range res.Diags {
		rx := want[d.Pos.Line]
		if rx == nil || !rx.MatchString(d.Msg) {
			t.Errorf("unexpected: %v", d)
			continue
		}
		got[d.Pos.Line] = true
	}
	for line, rx := range want {
		if !got[line] {
			t.Errorf("line %d: missing error %q", line, rx)
		}
	}
}

// The build tags of GOFLAGS, which ego generate -tags sets, count with the
// injectors' own.
func TestGoflagsTags(t *testing.T) {
	if res, err := Generate("testdata/tagged"); err != nil || res == nil || len(res.Diags) == 0 {
		t.Fatalf("without the tag: %v, %+v", err, res)
	}
	t.Setenv("GOFLAGS", "-mod=mod -tags=extra")
	res, err := Generate("testdata/tagged")
	if err != nil || res == nil || len(res.Diags) > 0 || res.Code == nil {
		t.Fatalf("with the tag: %v, %+v", err, res)
	}
}

func TestNoInjectors(t *testing.T) {
	if res, err := Generate("."); res != nil || err != nil {
		t.Fatal(res, err)
	}
}

// The wiring of internal/egotest/app matches its committed file.
func TestGolden(t *testing.T) {
	res, err := Generate("../egotest/app")
	if err != nil || res == nil || len(res.Diags) > 0 {
		t.Fatal(err, res)
	}
	want, _ := os.ReadFile(res.Path)
	if string(want) != string(res.Code) {
		t.Fatalf("%s is stale (run ego generate):\n%s", res.Path, res.Code)
	}
}

// The wiring of testdata/shared compiles, and a provider runs once in an
// injector, however many interfaces, types and lazy services need it.
func TestShared(t *testing.T) {
	res, err := Generate("testdata/shared")
	if err != nil || res == nil || len(res.Diags) > 0 {
		t.Fatal(err, res)
	}
	for _, fn := range strings.Split(string(res.Code), "\nfunc ") {
		if n := strings.Count(fn, "NewStore()"); n > 1 {
			t.Fatalf("NewStore called %d times:\n%s", n, fn)
		}
	}
	path, _ := filepath.Abs(res.Path)
	cfg := &packages.Config{Mode: packages.LoadSyntax, Dir: "testdata/shared", Overlay: map[string][]byte{path: res.Code}}
	if pkgs, err := packages.Load(cfg, "."); err != nil || packages.PrintErrors(pkgs) > 0 {
		t.Fatalf("%v\n%s", err, res.Code)
	}
}
