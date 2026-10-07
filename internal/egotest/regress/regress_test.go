package regress

import (
	"fmt"
	"io"
	"errors"
	"slices"
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
