package format_test

import (
	"bytes"
	"flag"
	stdformat "go/format"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/effect-go/effect-go/internal/syntax/format"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// raceEnabled reports whether the tests run with -race (race_test.go).
var raceEnabled bool

// Each testdata/format/x.input.ego formats to x.golden.ego.
func TestFormatGolden(t *testing.T) {
	inputs, _ := filepath.Glob("../testdata/format/*.input.ego")
	if len(inputs) == 0 {
		t.Fatal("no inputs")
	}
	for _, in := range inputs {
		src, err := os.ReadFile(in)
		if err != nil {
			t.Fatal(err)
		}
		got, err := format.Source(src)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		golden := strings.Replace(in, ".input.", ".golden.", 1)
		if *update {
			os.WriteFile(golden, got, 0o666)
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s:\n%s", golden, got)
		}
	}
}

// Formatting every .ego file in the repository twice changes nothing the
// second time.
func TestIdempotent(t *testing.T) {
	n := 0
	filepath.WalkDir("../../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == "errors" || strings.HasPrefix(d.Name(), ".")) && path != "../../.." {
			return filepath.SkipDir
		}
		if !strings.HasSuffix(path, ".ego") {
			return nil
		}
		src, _ := os.ReadFile(path)
		once, err := format.Source(src)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			return nil
		}
		if twice, _ := format.Source(once); !bytes.Equal(once, twice) {
			t.Errorf("%s: not idempotent\n--- once\n%s\n--- twice\n%s", path, once, twice)
		}
		n++
		return nil
	})
	if n < 10 {
		t.Fatalf("only %d .ego files", n)
	}
}

// ego fmt formats every Go file in the standard library exactly as gofmt.
// The files are independent, so they're formatted on every core. The race
// detector would only slow it tenfold: CI runs it without.
func TestStdlibLikeGofmt(t *testing.T) {
	if testing.Short() {
		t.Skip("formats the whole standard library")
	}
	if raceEnabled {
		t.Skip("no concurrency to check, and ten times slower with -race")
	}
	if !strings.HasPrefix(runtime.Version(), "go1.27") {
		t.Skip("the printer is a copy of Go 1.27's; other versions' gofmt may differ")
	}
	var paths []string
	filepath.WalkDir(filepath.Join(runtime.GOROOT(), "src"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".go") {
			paths = append(paths, path)
		}
		return nil
	})
	var n atomic.Int64
	var wg sync.WaitGroup
	next := make(chan string)
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for path := range next {
				src, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				want, err := stdformat.Source(src)
				if err != nil {
					continue // not valid Go (testdata)
				}
				got, err := format.Source(src)
				if err != nil {
					t.Errorf("%s: %v", path, err)
					continue
				}
				if !bytes.Equal(got, want) {
					t.Errorf("%s: differs from gofmt", path)
				}
				n.Add(1)
			}
		})
	}
	for _, p := range paths {
		next <- p
	}
	close(next)
	wg.Wait()
	t.Logf("%d files", n.Load())
	if n.Load() < 5000 {
		t.Fatalf("only %d files checked", n.Load())
	}
}
