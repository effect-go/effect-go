package scope

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// Main runs body in a scope whose context SIGINT and SIGTERM cancel, as a
// program's main function needs: when body returns, the scope's fibers have
// stopped and its resources are released. If body fails, Main logs the
// error with slog and exits with status 1.
//
//	func main() {
//		scope.Main(func(s *scope.Scope) error { return serve(s, cfg) })
//	}
func Main(body func(s *Scope) error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	_, err := Run(ctx, func(s *Scope) (struct{}, error) { return struct{}{}, body(s) })
	if err != nil {
		stop()
		slog.Error("stopped", slog.Any("error", err))
		os.Exit(1)
	}
}
