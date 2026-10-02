package main

import (
	"os/exec"
	"path/filepath"
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
