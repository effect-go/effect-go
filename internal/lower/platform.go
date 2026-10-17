package lower

import (
	"bytes"
	"cmp"
	"go/build"
	"go/build/constraint"
	"os"
	"slices"
	"strings"
)

// A .ego file that build constraints exclude here is compiled in a build
// context where they hold: the first of the ports below, the current one
// first, with the build tags of its //go:build line that it needs.

// ports are the GOOS/GOARCH pairs go builds for (go tool dist list).
var ports = strings.Fields(`aix/ppc64 android/386 android/amd64 android/arm
	android/arm64 darwin/amd64 darwin/arm64 dragonfly/amd64 freebsd/386
	freebsd/amd64 freebsd/arm freebsd/arm64 illumos/amd64 ios/amd64 ios/arm64
	js/wasm linux/386 linux/amd64 linux/arm linux/arm64 linux/loong64
	linux/mips linux/mips64 linux/mips64le linux/mipsle linux/ppc64
	linux/ppc64le linux/riscv64 linux/s390x netbsd/386 netbsd/amd64 netbsd/arm
	netbsd/arm64 openbsd/386 openbsd/amd64 openbsd/arm openbsd/arm64
	openbsd/ppc64 openbsd/riscv64 plan9/386 plan9/amd64 plan9/arm
	solaris/amd64 wasip1/wasm windows/386 windows/amd64 windows/arm64`)

// maxTagSets bounds the sets of build tags tried on each port.
const maxTagSets = 256

// contextFor returns a build context that includes the .ego file name, with
// source src, in dir: here with other build tags, else here for another
// port, the most common first and with here's GOARCH if it can, with as few
// changes to the build tags as it needs. It reports false if no port and
// tags of the file's constraints include it.
func contextFor(here build.Context, dir, name string, src []byte) (build.Context, bool) {
	free := freeTags(src)
	sets := flips(len(free), maxTagSets)
	for _, port := range portOrder(here.GOOS, here.GOARCH) {
		goos, goarch, _ := strings.Cut(port, "/")
		for _, set := range sets {
			tags := slices.Clone(here.BuildTags)
			for _, i := range set {
				if j := slices.Index(tags, free[i]); j >= 0 {
					tags = slices.Delete(tags, j, j+1)
				} else {
					tags = append(tags, free[i])
				}
			}
			slices.Sort(tags)
			ctx := portContext(here, goos, goarch, tags)
			if matchBuild(ctx, dir, name, src) {
				return ctx, true
			}
		}
	}
	return build.Context{}, false
}

// portOrder returns the ports in the order they are tried: the GOOS goos,
// then linux, darwin and windows, then the others; and for each, the GOARCH
// goarch, then amd64 and arm64, then the others.
func portOrder(goos, goarch string) []string {
	rank := func(v string, first ...string) int {
		if i := slices.Index(first, v); i >= 0 {
			return i
		}
		return len(first)
	}
	list := slices.Clone(ports)
	slices.SortFunc(list, func(a, b string) int {
		ao, aa, _ := strings.Cut(a, "/")
		bo, ba, _ := strings.Cut(b, "/")
		return cmp.Or(
			cmp.Compare(rank(ao, goos, "linux", "darwin", "windows"), rank(bo, goos, "linux", "darwin", "windows")),
			strings.Compare(ao, bo),
			cmp.Compare(rank(aa, goarch, "amd64", "arm64"), rank(ba, goarch, "amd64", "arm64")),
			strings.Compare(aa, ba))
	})
	return list
}

// portContext returns here for another port and build tags. As go does,
// cgo is off for another port unless CGO_ENABLED=1 asks for it.
func portContext(here build.Context, goos, goarch string, tags []string) build.Context {
	ctx := here
	ctx.GOOS, ctx.GOARCH, ctx.BuildTags = goos, goarch, tags
	if goos != here.GOOS || goarch != here.GOARCH {
		ctx.CgoEnabled = os.Getenv("CGO_ENABLED") == "1"
	}
	if goarch != here.GOARCH {
		// The tags of the GOARCH's features, as amd64.v1, are another's.
		ctx.ToolTags = slices.DeleteFunc(slices.Clone(here.ToolTags), func(t string) bool { return strings.HasPrefix(t, here.GOARCH+".") })
	}
	return ctx
}

// freeTags returns, sorted, the tags of a .ego file's build constraints
// that build tags may set: not a GOOS or GOARCH, a release, cgo or the
// compiler, which come with the port and the toolchain, nor ignore, which
// marks files never built, as go run scripts.
func freeTags(src []byte) []string {
	var tags []string
	var walk func(x constraint.Expr)
	walk = func(x constraint.Expr) {
		switch x := x.(type) {
		case *constraint.TagExpr:
			t := x.Tag
			if !strings.Contains(t, ".") && !slices.Contains(knownOS, t) && !slices.Contains(knownArch, t) &&
				!slices.Contains([]string{"unix", "cgo", "gc", "gccgo", "boringcrypto", "ignore"}, t) && !slices.Contains(tags, t) {
				tags = append(tags, t)
			}
		case *constraint.NotExpr:
			walk(x.X)
		case *constraint.AndExpr:
			walk(x.X)
			walk(x.Y)
		case *constraint.OrExpr:
			walk(x.X)
			walk(x.Y)
		}
	}
	block := false // in a /* comment */
	for line := range bytes.Lines(src) {
		s := strings.TrimSpace(string(line))
		if block {
			block = !strings.Contains(s, "*/")
			continue
		}
		switch {
		case s == "":
		case strings.HasPrefix(s, "/*"):
			block = !strings.Contains(s[2:], "*/")
		case strings.HasPrefix(s, "//"):
			if constraint.IsGoBuild(s) || constraint.IsPlusBuild(s) {
				if x, err := constraint.Parse(s); err == nil {
					walk(x)
				}
			}
		default:
			slices.Sort(tags)
			return tags // the package clause
		}
	}
	slices.Sort(tags)
	return tags
}

// flips returns the subsets of n tags to flip, by index, at most limit
// of them: none first, then one, then two, and so on.
func flips(n, limit int) [][]int {
	sets := [][]int{{}}
	var add func(set []int, from, size int)
	add = func(set []int, from, size int) {
		if len(set) == size {
			if len(sets) < limit {
				sets = append(sets, slices.Clone(set))
			}
			return
		}
		for i := from; i < n && len(sets) < limit; i++ {
			add(append(set, i), i+1, size)
		}
	}
	for size := 1; size <= n && len(sets) < limit; size++ {
		add(nil, 0, size)
	}
	return sets
}

// platform returns the importer of the packages as ctx sees them: one per
// build context, kept with im.
func (im *Importer) platform(ctx build.Context) *Importer {
	cgo := "0"
	if ctx.CgoEnabled {
		cgo = "1"
	}
	tags := "-tags=" + strings.Join(ctx.BuildTags, ",") // replaces GOFLAGS's
	key := ctx.GOOS + "/" + ctx.GOARCH + " CGO_ENABLED=" + cgo + " " + tags
	im.mu.Lock()
	defer im.mu.Unlock()
	if o := im.others[key]; o != nil {
		return o
	}
	o := NewImporter()
	o.env = append(os.Environ(), "GOOS="+ctx.GOOS, "GOARCH="+ctx.GOARCH, "CGO_ENABLED="+cgo)
	o.flags = []string{tags}
	if im.others == nil {
		im.others = map[string]*Importer{}
	}
	im.others[key] = o
	return o
}
