package main

import (
	"os"
	"path/filepath"
	"testing"
)

// ego fmt -w ./... formats the .ego files below the current directory.
func TestFmtRecursive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "a.ego")
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package a\nfunc  f( ) {}\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if err := fmtCmd([]string{"-w", "./..."}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "package a\n\nfunc f() {}\n" {
		t.Fatalf("not formatted:\n%s", got)
	}
}
