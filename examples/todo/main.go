package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/effect-go/effect-go/scope"
)

// main is plain Go: it builds the app with the generated BuildCLI and runs
// it in a scope, which closes the database pool when the command ends.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cfg := Config{DatabaseURL: databaseURL(), Out: os.Stdout, Err: os.Stderr}
	code, err := scope.Run(ctx, func(s *scope.Scope) (int, error) {
		cli, err := BuildCLI(s.Context(), s, cfg)
		if err != nil {
			return 3, err
		}
		return cli.Run(s.Context(), os.Args[1:]), nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(code)
}
