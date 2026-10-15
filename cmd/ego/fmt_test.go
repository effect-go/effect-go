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

// As with gofmt, a directory needs -l or -w, and a file that can't be
// formatted or written doesn't stop the others.
func TestFmtArgs(t *testing.T) {
	module(t, map[string]string{
		"a.ego": "package a\nfunc  f( ) {}\n",
		"b.ego": "package b\nfunc (\n",
		"c.ego": "package c\nfunc  g( ) {}\n",
	})
	if err := os.Chmod("a.ego", 0o444); err != nil {
		t.Fatal(err)
	}
	if err := fmtCmd([]string{"."}); err == nil {
		t.Error("a directory without -l or -w: no error")
	}
	if err := fmtCmd([]string{"-w", "."}); err == nil {
		t.Error("a.ego is read-only and b.ego doesn't parse: no error")
	}
	if got, _ := os.ReadFile("c.ego"); string(got) != "package c\n\nfunc g() {}\n" {
		t.Errorf("c.ego not formatted:\n%s", got)
	}
}
