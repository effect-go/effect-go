package main

import (
	"bytes"
	"os"
	"path/filepath"
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

// copyPackage copies files of a package into a new directory in the module,
// so that it builds; go ignores directories starting with _ in patterns
// such as ./...
func copyPackage(t *testing.T, from string, files ...string) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", "_pkg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
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
