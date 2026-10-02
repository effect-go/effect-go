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
	pkg := "../../internal/egotest/status"
	gen := filepath.Join(pkg, "status_ego.go")
	before, err := os.ReadFile(gen)
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(t.TempDir(), "c.out")
	if err := testCmd([]string{"-count=1", "-coverprofile=" + profile, pkg}); err != nil {
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
