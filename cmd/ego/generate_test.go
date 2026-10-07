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
