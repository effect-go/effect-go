package lower

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// benchFile is a typical .ego file, with names made unique by %[1]d.
const benchFile = `package bench

import (
	"slices"
	"strconv"
)

error E%[1]d {
	A%[1]d{ Reason string } "a {Reason}"
	B%[1]d{ Cause error }
}

type S%[1]d struct{ n int }

effect (s *S%[1]d) Load(id int) (int, E%[1]d) {
	n := check strconv.Atoi(f"{id}") as B%[1]d
	if n < 0 {
		fail A%[1]d{Reason: "negative"}
	}
	return n + s.n, nil
}

effect (s *S%[1]d) Both(a, b int) (int, error) {
	x, y := check all(s.Load(a), s.Load(b))
	z := check retry(schedule.Recurs(2), s.Load(x + y))
	return z, nil
}

func Code%[1]d(err E%[1]d) int {
	return match err { nil => 0; A%[1]d(_) => 1; B%[1]d(_) => 2 }
}

func Names%[1]d(xs []string) []string {
	return slices.DeleteFunc(xs, x => x == "")
}
`

// BenchmarkEditor measures what the editor proxy does after each pause in
// typing: regenerate a package, with loaded dependencies cached and no
// type-check of the output.
func BenchmarkEditor(b *testing.B) {
	for _, files := range []int{10, 50, 200} {
		b.Run(fmt.Sprintf("%d_lines", files*strings.Count(benchFile, "\n")), func(b *testing.B) {
			dir, err := os.MkdirTemp(".", "_bench")
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { os.RemoveAll(dir) })
			for i := range files {
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.ego", i)), fmt.Appendf(nil, benchFile, i), 0o666); err != nil {
					b.Fatal(err)
				}
			}
			im := NewImporter()
			cfg := Config{Dir: dir, NoTypeCheck: true, Importer: im}
			if res, err := Generate(cfg); err != nil || len(res.Diags) > 0 {
				b.Fatal(err, res.Diags)
			}
			b.ResetTimer()
			for b.Loop() {
				if _, err := Generate(cfg); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkGenerate measures ego generate on the fixtures, from scratch:
// loading the dependencies, then compiling every package. A load per
// package, instead of one for all, shows up here.
func BenchmarkGenerate(b *testing.B) {
	dirs := fixtures(b)
	for b.Loop() {
		im := NewImporter()
		if err := im.Preload(dirs); err != nil {
			b.Fatal(err)
		}
		for _, dir := range dirs {
			if _, err := Generate(Config{Dir: dir, Importer: im}); err != nil {
				b.Fatal(err)
			}
		}
	}
}
