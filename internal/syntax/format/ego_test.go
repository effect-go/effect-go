package format_test

import (
	"bytes"
	stdformat "go/format"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/effect-go/effect-go/internal/syntax/format"
)

// The dialect samples format without error, and formatting is idempotent.
func TestEgoIdempotent(t *testing.T) {
	files, _ := filepath.Glob("../testdata/*.ego")
	if len(files) == 0 {
		t.Fatal("no samples")
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		once, err := format.Source(src)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		twice, err := format.Source(once)
		if err != nil {
			t.Errorf("%s (formatted): %v\n%s", f, err, once)
			continue
		}
		if !bytes.Equal(once, twice) {
			t.Errorf("%s: not idempotent\n--- once\n%s\n--- twice\n%s", f, once, twice)
		}
		if testing.Verbose() {
			t.Logf("%s:\n%s", f, once)
		}
	}
}

// ego fmt formats every Go file in the standard library exactly as gofmt.
func TestStdlibLikeGofmt(t *testing.T) {
	if testing.Short() {
		t.Skip("formats the whole standard library")
	}
	if !strings.HasPrefix(runtime.Version(), "go1.27") {
		t.Skip("the printer is a copy of Go 1.27's; other versions' gofmt may differ")
	}
	root := filepath.Join(runtime.GOROOT(), "src")
	n := 0
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		want, err := stdformat.Source(src)
		if err != nil {
			return nil // not valid Go (testdata)
		}
		got, err := format.Source(src)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			return nil
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: differs from gofmt", path)
		}
		n++
		return nil
	})
	t.Logf("%d files", n)
	if n < 5000 {
		t.Fatalf("only %d files checked", n)
	}
}
