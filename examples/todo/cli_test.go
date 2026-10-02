package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/effect-go/effect-go/scope"
)

var today = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)

// session runs commands against one app, like successive shell commands
// against one database.
type session struct {
	t   *testing.T
	cli *CLI
	out *bytes.Buffer
}

// run runs a command and returns its output and exit code.
func (s *session) run(args ...string) (string, int) {
	s.out.Reset()
	code := s.cli.Run(s.t.Context(), args)
	return s.out.String(), code
}

// withApp builds the app with build and runs fn inside its scope.
func withApp(t *testing.T, build func(context.Context, *scope.Scope, Config) (*CLI, error), fn func(*session)) {
	out := &bytes.Buffer{}
	_, err := scope.Run(t.Context(), func(s *scope.Scope) (struct{}, error) {
		cli, err := build(s.Context(), s, Config{DatabaseURL: os.Getenv("TODO_TEST_DATABASE_URL"), Out: out, Err: out})
		if err != nil {
			return struct{}{}, err
		}
		fn(&session{t, cli, out})
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func memory(ctx context.Context, s *scope.Scope, cfg Config) (*CLI, error) {
	return BuildTestCLI(ctx, s, cfg, func() time.Time { return today })
}

// The commands, on the in-memory graph.
func TestCommands(t *testing.T) { withApp(t, memory, commands) }

// The same commands on PostgreSQL, if TODO_TEST_DATABASE_URL names a
// database the test may empty.
func TestPostgres(t *testing.T) {
	url := os.Getenv("TODO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TODO_TEST_DATABASE_URL to run against PostgreSQL")
	}
	withApp(t, BuildCLI, func(s *session) {
		pool, err := Connect(t.Context(), Config{DatabaseURL: url})
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		if _, err := pool.Exec(t.Context(), "TRUNCATE todos RESTART IDENTITY"); err != nil {
			t.Fatal(err)
		}
		commands(s)
	})
}

func commands(s *session) {
	t := s.t
	expect := func(args string, wantCode int, want ...string) {
		t.Helper()
		out, code := s.run(strings.Fields(args)...)
		if code != wantCode {
			t.Errorf("todo %s: exit %d, want %d\n%s", args, code, wantCode, out)
		}
		for _, w := range want {
			if !strings.Contains(out, w) {
				t.Errorf("todo %s: output lacks %q:\n%s", args, w, out)
			}
		}
	}
	expect("list", 0, "nothing to do")
	expect("add buy milk -p high -due 2026-10-01", 0, "added #1: buy milk")
	expect("add -due 2026-10-20 write the report", 0, "added #2: write the report")
	expect("add -p low water plants", 0, "added #3")
	expect("list", 0, "#1   High   buy milk  2026-10-01 (overdue)")
	if out, _ := s.run("list"); strings.Index(out, "#2") > strings.Index(out, "#3") {
		t.Errorf("medium should come before low:\n%s", out)
	}
	expect("stats", 0, "3 open, 0 done, 1 overdue")
	expect("done 1", 0)
	expect("done 1", 0) // done twice is fine
	if out, _ := s.run("list"); strings.Contains(out, "buy milk") {
		t.Errorf("list shows a done todo:\n%s", out)
	}
	expect("list -all", 0, "[x] #1")
	expect("stats", 0, "2 open, 1 done, 0 overdue")
	expect("rm 3", 0)
	expect("rm 3", 1, "no todo #3")
	expect("done 99", 1, "no todo #99")
	expect("done x", 2, `bad id "x"`)
	expect("add -p urgent x", 2, `unknown priority "urgent"`)
	expect("add -due tomorrow x", 2, `bad due date "tomorrow"`)
	expect("add", 2, "a todo needs a title")
	expect("frobnicate", 2, "usage:")
	expect("", 2, "usage:")
}

// A database that never answers is a readable error, after the retries.
func TestNoDatabase(t *testing.T) {
	_, err := scope.Run(t.Context(), func(s *scope.Scope) (*CLI, error) {
		return BuildCLI(s.Context(), s, Config{DatabaseURL: "postgres://nobody@127.0.0.1:1/todo?connect_timeout=1"})
	})
	if err == nil || !strings.Contains(err.Error(), "connect to the database") {
		t.Fatalf("err %v", err)
	}
	_, err = scope.Run(t.Context(), func(s *scope.Scope) (*CLI, error) {
		return BuildCLI(s.Context(), s, Config{DatabaseURL: "not a url"})
	})
	if err == nil || !strings.Contains(err.Error(), "bad database URL") {
		t.Fatalf("err %v", err)
	}
}
