package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ego test -cover reports the generated Go, which go tool cover reads, and
// leaves the committed files as they were.
func TestCoverage(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	// A copy, as the run rewrites the generated files for a while.
	dir := copyPackage(t, "../../internal/egotest/status", "status.ego", "status_test.go")
	if err := generate([]string{dir}); err != nil {
		t.Fatal(err)
	}
	gen := filepath.Join(dir, "status_ego.go")
	before, err := os.ReadFile(gen)
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(t.TempDir(), "c.out")
	if err := testCmd([]string{"-count=1", "-coverprofile=" + profile, "./" + dir}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "/status_ego.go:") || strings.Contains(string(data), ".ego:") {
		t.Errorf("profile names the wrong files:\n%.300s", data)
	}
	if after, _ := os.ReadFile(gen); !bytes.Equal(before, after) {
		t.Error("the generated file was not restored")
	}
}

// runtimeModule makes the current directory a new module, which can import
// this module's packages through a go.work file.
func runtimeModule(t *testing.T) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":  "module example.com/tmp\n\ngo 1.26.0\n",
		"go.work": "go 1.26.0\n\nuse (\n\t.\n\t" + root + "\n)\n",
	})
	t.Chdir(dir)
}

// copyPackage copies files of a package into a new module, which becomes
// the current directory, and returns the package's directory there.
func copyPackage(t *testing.T, from string, files ...string) string {
	t.Helper()
	from, err := filepath.Abs(from)
	if err != nil {
		t.Fatal(err)
	}
	runtimeModule(t)
	dir := filepath.Base(from)
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(from, f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), data, 0o666); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Flag values and what follows -args aren't packages.
func TestTestPackages(t *testing.T) {
	cases := []struct {
		args []string
		dir  string
		want []string
		tags string
	}{
		{nil, "", []string{"."}, ""},
		{[]string{"-run", "./x", "./a/...", "-v", "-count=1"}, "", []string{"./a/..."}, ""},
		{[]string{"-coverprofile", "./c.out", "."}, "", []string{"."}, ""},
		{[]string{"./b", "-args", "./c"}, "", []string{"./b"}, ""},
		{[]string{"-c", "./a", "-artifacts", "./b", "-buildvcs", "./c", "-test.v", "./d"}, "", []string{"./a", "./b", "./c", "./d"}, ""},
		{[]string{"-C", "sub", "-tags", "integration", "./..."}, "sub", []string{"./..."}, "integration"},
		{[]string{"-C=sub", "-tags=a,b", "example.com/m/x"}, "sub", []string{"example.com/m/x"}, "a,b"},
	}
	for _, c := range cases {
		dir, got, tags := testPackages(c.args)
		if dir != c.dir || !slices.Equal(got, c.want) || tags != c.tags {
			t.Errorf("testPackages(%q) = %q, %q, %q; want %q, %q, %q", c.args, dir, got, tags, c.dir, c.want, c.tags)
		}
	}
}

// ego test -C dir generates the packages of dir, and -tags those of its
// tagged files.
func TestTestFlags(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":               "module example.com/flags\n\ngo 1.26\n",
		"a.ego":                "package a\n\nfunc A() int { return 1 }\n",
		"a_test.go":            "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tif A() != 1 {\n\t\tt.Fatal()\n\t}\n}\n",
		"integration_test.ego": "//go:build integration\n\npackage a\n\nimport \"testing\"\n\nfunc TestIntegration(t *testing.T) {}\n",
	})
	t.Chdir(t.TempDir())
	if err := testCmd([]string{"-C", dir, "-count=1", "-tags", "integration", "./..."}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a_ego.go", "integration_ego_test.go"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Error(err)
		}
	}
}
