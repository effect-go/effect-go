package regress

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/effect-go/effect-go/internal/egotest/regress/disk"
)

func TestErrKept(t *testing.T) {
	if n, err := CheckKeepsErr("x", "2"); err == nil {
		t.Errorf("CheckKeepsErr lost the first error: %d", n)
	}
	if n, err := ElseKeepsErr("1", "x"); n != 1 || err != nil {
		t.Errorf("ElseKeepsErr: %d %v", n, err)
	}
	if n, err := ElseKeepsNamedErr("x"); n != 7 || err != nil {
		t.Errorf("ElseKeepsNamedErr: %d %v", n, err)
	}
}

func TestPercent(t *testing.T) {
	if _, err := Percent(); err.Error() != "disk 100% full" {
		t.Errorf("fail: %q", err)
	}
	if s := PercentF(); s != "100%" {
		t.Errorf("f-string: %q", s)
	}
	if s := (disk.Full{}).Error(); s != "disk 100% full" {
		t.Errorf("case message: %q", s)
	}
	if s := (disk.Gone{}).Error(); s != "gone" {
		t.Errorf("default message: %q", s)
	}
}

func TestSentinel(t *testing.T) {
	if got := Sentinel(fmt.Errorf("read: %w", io.EOF)); got != "eof" {
		t.Errorf("wrapped io.EOF: %s", got)
	}
}

func TestOptAssign(t *testing.T) {
	if got := OptAssign([]*User{{"a"}, nil}); !slices.Equal(got, []string{"a", ""}) {
		t.Errorf("got %q", got)
	}
}

func TestPassThrough(t *testing.T) {
	if _, ok := errors.AsType[Duplicate](PassThrough("a")); !ok {
		t.Fatalf("got %v, want the Duplicate itself", PassThrough("a"))
	}
	if u, ok := PassThrough("a").(Unavailable); ok {
		t.Fatalf("wrapped: %v", u)
	}
}

func TestEvaluation(t *testing.T) {
	if ShortCircuit(nil) || !ShortCircuit(&Box{N: 1}) {
		t.Error("ShortCircuit")
	}
	if got := Order(); !slices.Equal(got, []string{"left", "cond"}) {
		t.Errorf("order %q", got)
	}
}

func TestShadow(t *testing.T) {
	if got := Shadow(io.EOF); got != "outer" {
		t.Errorf("_ arm saw %q", got)
	}
	if got := Shadow(Duplicate{Slug: "a"}); got != "a" {
		t.Errorf("Duplicate arm: %q", got)
	}
}

// A panic in a one-line effect body reports the .ego line.
func TestOneLineBody(t *testing.T) {
	if testing.CoverMode() != "" {
		t.Skip("ego test -cover reports the generated Go's lines")
	}
	src, _ := os.ReadFile("regress.ego")
	line := slices.IndexFunc(strings.Split(string(src), "\n"), func(l string) bool { return strings.HasSuffix(l, "// one-line") }) + 1
	defer func() {
		recover()
		if want := "regress.ego:" + strconv.Itoa(line); !strings.Contains(string(debug.Stack()), want) {
			t.Errorf("stack doesn't name %s:\n%s", want, debug.Stack())
		}
	}()
	DivOneLine(t.Context(), map[string]int{})
}

func TestOptFallback(t *testing.T) {
	if s, h := OptFallback(nil); s != 0 || h != "none" {
		t.Errorf("nil: %v %v", s, h)
	}
	if s, h := OptFallback(&Player{Score: 1.5, Home: "Lyon"}); s != 1.5 || h != "Lyon" {
		t.Errorf("set: %v %v", s, h)
	}
}

func TestFStringLoop(t *testing.T) {
	if n := FStringLoop("x"); n != 2 {
		t.Errorf("got %d", n)
	}
}

func TestGenericLambda(t *testing.T) {
	if got := GenericLambda([]User{{"a"}, {"b"}}); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("got %q", got)
	}
}

func TestLocalNames(t *testing.T) {
	if n, err := LocalNames("2"); n != 3 || err != nil {
		t.Errorf("got %d %v", n, err)
	}
	if _, err := LocalNames("x"); err == nil {
		t.Error("no error")
	}
}

func TestParenReceiver(t *testing.T) {
	if n, err := ParenReceiver("2"); n != 3 || err != nil {
		t.Errorf("got %d %v", n, err)
	}
}
