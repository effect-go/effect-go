package scope

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// Main runs body in a scope whose context SIGINT and SIGTERM cancel, as a
// program's main function needs: when body returns, the scope's fibers have
// stopped and its resources are released. A second signal kills the
// program, for a shutdown that hangs. If body fails, or the shutdown does
// (a resource fails to close, or fibers outlive a StopTimeout), Main logs
// the error with slog and exits with status 1; body failing with the
// cancellation the signal caused is a clean stop. opts are Run's, such as
// StopTimeout.
//
//	func main() {
//		scope.Main(func(s *scope.Scope) error { return serve(s, cfg) })
//	}
func Main(body func(s *Scope) error, opts ...Option) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// After the first signal, the default handling comes back.
	context.AfterFunc(ctx, stop)
	var err error
	// Run's error is then the shutdown's only.
	_, serr := Run(ctx, func(s *Scope) (struct{}, error) { err = body(s); return struct{}{}, nil }, opts...)
	if err != nil && ctx.Err() != nil && KindOf(err) == Interrupt {
		err = nil
	}
	if err = errors.Join(err, serr); err != nil {
		stop()
		slog.Error("stopped", slog.Any("error", err))
		os.Exit(1)
	}
}
