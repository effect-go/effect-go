package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// An ejected package is plain Go that passes its tests, with no .ego files
// left.
func TestEject(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := copyPackage(t, "../../internal/egotest/report", "report.ego", "report_test.go")
	if err := ejectCmd([]string{"-w", dir}); err != nil {
		t.Fatal(err)
	}
	if ego, _ := filepath.Glob(filepath.Join(dir, "*.ego")); len(ego) > 0 {
		t.Errorf("left %v", ego)
	}
	if out, err := exec.Command("go", "test", "./"+dir).CombinedOutput(); err != nil {
		t.Fatalf("the ejected package fails: %v\n%s", err, out)
	}
}

// Ejecting compiles each package against the current Go of those it
// imports; a file that build constraints exclude here is compiled where they
// hold, as the others are, without //line directives.
func TestEjectStale(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go build")
	}
	other := "linux"
	if runtime.GOOS == other {
		other = "darwin"
	}
	module(t, map[string]string{
		"go.mod":  "module example.com/ej\n\ngo 1.26\n",
		"a/a.ego": "package a\n\nfunc A() int { return 1 }\n",
		"b/b.ego": "package b\n\nimport \"example.com/ej/a\"\n\nfunc B() int { return a.A() }\n",
	})
	if err := generate(nil); err != nil {
		t.Fatal(err)
	}
	// a gains a function that b uses, and neither is generated again.
	writeFiles(t, ".", map[string]string{
		"a/a.ego":               "package a\n\nfunc A() int { return 1 }\n\nfunc A2() int { return 2 }\n",
		"b/b.ego":               "package b\n\nimport \"example.com/ej/a\"\n\nfunc B() int { return a.A2() }\n",
		"a/x_" + other + ".ego": "package a\n\nenum Shape { Circle{ R float64 }; Dot }\n",
	})
	if err := ejectCmd([]string{"./..."}); err == nil {
		t.Error("the plan ignored the stale Go")
	}
	if err := ejectCmd([]string{"-w", "./..."}); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "./...").CombinedOutput(); err != nil {
		t.Fatalf("the ejected module fails: %v\n%s", err, out)
	}
	// The committed Go, without its //line directives, would keep the
	// empty comment line that separated one from a doc comment.
	x, err := os.ReadFile("a/x_" + other + ".go")
	if s := string(x); err != nil || strings.Contains(s, "//line") || strings.Contains(s, "Code generated") || !strings.Contains(s, "//go:build "+other) || strings.Contains(s, "\n//\n") {
		t.Errorf("a/x_%s.go: %v\n%s", other, err, x)
	}
}
