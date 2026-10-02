//go:build unix

package scope

import (
	"context"
	"syscall"
	"testing"
	"time"
)

// SIGTERM cancels Main's scope, and Main returns once its resources are
// released.
func TestMainStopsOnSignal(t *testing.T) {
	released := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		Main(func(s *Scope) error {
			s.Defer(func(ctx context.Context) error { released = true; return nil })
			syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
			<-s.Context().Done()
			return nil
		})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Main didn't stop on SIGTERM")
	}
	if !released {
		t.Fatal("the scope's resources weren't released")
	}
}
