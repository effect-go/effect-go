package layer_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/effect-go/effect-go/layer"
)

// A failed build is tried again at the next Get; concurrent Gets then
// share one build.
func TestLazy(t *testing.T) {
	var builds atomic.Int32
	l := layer.NewLazy(func() (int, error) {
		if builds.Add(1) == 1 {
			return 0, errors.New("down")
		}
		return 42, nil
	})
	if _, err := l.Get(); err == nil {
		t.Fatal("no error")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if v, err := l.Get(); v != 42 || err != nil {
				t.Error(v, err)
			}
		})
	}
	wg.Wait()
	if n := builds.Load(); n != 2 {
		t.Fatalf("%d builds", n)
	}
}
