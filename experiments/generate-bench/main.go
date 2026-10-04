// Command generate-bench times ego generate on real code: the wait users see
// on the command line and in CI. For each module directory it reports the
// median of several runs of ego generate -check ./... (which writes
// nothing), and of go build ./... for scale.
//
//	go run ./experiments/generate-bench . examples ~/src/some-service
//
// Without arguments it measures this repository and its examples. It uses the
// ego on the PATH, or the one -ego names.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

func main() {
	ego := flag.String("ego", "ego", "the ego command to time")
	runs := flag.Int("n", 5, "runs per measurement")
	flag.Parse()
	dirs := flag.Args()
	if len(dirs) == 0 {
		dirs = []string{".", "examples"}
	}
	fmt.Printf("%-40s %6s %8s %10s %10s\n", "module", "files", "lines", "generate", "go build")
	for _, dir := range dirs {
		files, lines := count(dir)
		gen := median(*runs, dir, *ego, "generate", "-check", "./...")
		build := median(*runs, dir, "go", "build", "./...")
		fmt.Printf("%-40s %6d %8d %10v %10v\n", dir, files, lines, gen, build)
	}
}

// median runs the command in dir n times, after one run to warm caches, and
// returns the median wall time.
func median(n int, dir string, name string, args ...string) time.Duration {
	var ts []time.Duration
	for i := range n + 1 {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		start := time.Now()
		// Stale generated code is still a full run: time it all the same.
		if out, err := cmd.CombinedOutput(); err != nil && !strings.Contains(string(out), ": stale; run ego generate") {
			fmt.Fprintf(os.Stderr, "%s: %s %s: %v\n%s", dir, name, strings.Join(args, " "), err, out)
			os.Exit(1)
		}
		if i > 0 {
			ts = append(ts, time.Since(start))
		}
	}
	slices.Sort(ts)
	return ts[len(ts)/2].Round(10 * time.Millisecond)
}

// count returns the number of .ego files under dir, in its own module, and
// their lines.
func count(dir string) (files, lines int) {
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && path != dir {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
				return filepath.SkipDir
			}
		}
		if strings.HasSuffix(path, ".ego") {
			src, _ := os.ReadFile(path)
			files++
			lines += strings.Count(string(src), "\n")
		}
		return nil
	})
	return files, lines
}
