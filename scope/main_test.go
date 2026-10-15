//go:build unix

package scope

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
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

// A shutdown that fails makes Main log and exit with status 1, though body
// stopped cleanly: here a release that ran out of time.
func TestMainFailsOnReleaseError(t *testing.T) {
	if os.Getenv("SCOPE_MAIN") != "" {
		Main(func(s *Scope) error {
			s.Defer(func(ctx context.Context) error { return fmt.Errorf("shutdown: %w", context.DeadlineExceeded) })
			syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
			<-s.Context().Done()
			return s.Context().Err()
		})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainFailsOnReleaseError$")
	cmd.Env = append(os.Environ(), "SCOPE_MAIN=1")
	out, err := cmd.CombinedOutput()
	if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 1 || !strings.Contains(string(out), "shutdown: context deadline exceeded") {
		t.Fatalf("%v, output:\n%s", err, out)
	}
}
