package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A new project builds and passes its tests.
func TestNew(t *testing.T) {
	if testing.Short() {
		t.Skip("builds ego and a project")
	}
	tmp := t.TempDir()
	ego := filepath.Join(tmp, "ego")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run(".", "go", "build", "-o", ego, ".")
	root, _ := filepath.Abs("../..")
	run(tmp, ego, "new", "-replace", root, "example.com/hello")
	run(filepath.Join(tmp, "hello"), ego, "test", "./...")
}

// The project template ships the language guide as it is in the repository.
func TestTemplateGuide(t *testing.T) {
	want, _ := os.ReadFile("../../AGENTS.md")
	got, _ := os.ReadFile("testdata/template/AGENTS.md")
	if !bytes.Equal(got, want) {
		t.Fatal("testdata/template/AGENTS.md differs from AGENTS.md: copy it")
	}
}
