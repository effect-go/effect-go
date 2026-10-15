package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A fresh module, with no generated Go yet, whose packages import each
// other's .ego code, and an external test that imports a package importing
// its own: one ego generate is enough.
func TestGenerateConverges(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":   "module example.com/conv\n\ngo 1.26\n",
		"main.ego": "package main\n\nimport \"example.com/conv/b\"\n\nfunc main() { println(b.B()) }\n",
		"a/a.ego":  "package a\n\nfunc A() int { return 1 }\n",
		"b/b.ego":  "package b\n\nimport \"example.com/conv/a\"\n\nfunc B() int { return a.A() + 1 }\n",
		"a/a_test.ego": `package a_test

import (
	"testing"

	"example.com/conv/a"
	"example.com/conv/b"
)

func TestA(t *testing.T) {
	if b.B() != a.A()+1 {
		t.Fatal(b.B())
	}
}
`,
	}
	for name, src := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	if err := generate([]string{"./..."}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	defer func() { checkOnly, stale = false, false }()
	if err := generate([]string{"-check", "./..."}); err != nil {
		t.Fatalf("a second run still had work: %v", err)
	}
}

// Deleting or renaming a .ego file removes the Go generated from it, and
// -check reports it until then. Hand-written *_ego.go files stay.
func TestGenerateRemovesLeftovers(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/left\n\ngo 1.26\n",
		"a/a.ego":       "package a\n\nfunc A() int { return 1 }\n",
		"a/old.ego":     "package a\n\nfunc Old() int { return 2 }\n",
		"a/hand_ego.go": "package a\n\nfunc Hand() int { return 3 }\n",
		"gone/gone.ego": "package gone\n",
	}
	for name, src := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	if err := generate([]string{"./..."}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove("gone/gone.ego"); err != nil {
		t.Fatal(err)
	}
	err := generate([]string{"-check", "./..."})
	checkOnly, stale = false, false
	if err == nil {
		t.Fatal("-check passed with a leftover")
	}
	if err := os.Rename("a/old.ego", "a/new.ego"); err != nil {
		t.Fatal(err)
	}
	if err := generate([]string{"./..."}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{"a/old_ego.go": false, "gone/gone_ego.go": false, "a/new_ego.go": true, "a/hand_ego.go": true} {
		if exists(path) != want {
			t.Errorf("%s exists: %v, want %v", path, !want, want)
		}
	}
}
