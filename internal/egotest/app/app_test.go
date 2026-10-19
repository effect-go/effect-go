package app

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/effect-go/effect-go/scope"
)

func TestShutdownOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := &Log{}
		name, err := scope.Run(t.Context(), func(s *scope.Scope) (string, error) {
			app, err := BuildApp(s.Context(), s, Config{DSN: "postgres://", Log: log})
			if err != nil {
				return "", err
			}
			log.Add("serving")
			return app.Service.Repo.Name(), nil
		})
		if err != nil || name != "postgres" {
			t.Fatal(name, err)
		}
		want := []string{"db opened", "cache started", "serving", "cache stopped", "db closed"}
		if !slices.Equal(log.Events, want) {
			t.Fatalf("events %q, want %q", log.Events, want)
		}
	})
}

func TestSwapProvider(t *testing.T) {
	log := &Log{}
	name, err := scope.Run(context.Background(), func(s *scope.Scope) (string, error) {
		app, err := BuildTestApp(s.Context(), s, Config{Log: log})
		if err != nil {
			return "", err
		}
		return app.Service.Repo.Name(), nil
	})
	// No DSN is needed: the database isn't part of the test graph.
	if err != nil || name != "memory" || slices.Contains(log.Events, "db opened") {
		t.Fatal(name, err, log.Events)
	}
}

func TestProviderError(t *testing.T) {
	_, err := scope.Run(context.Background(), func(s *scope.Scope) (*App, error) {
		return BuildApp(s.Context(), s, Config{Log: &Log{}})
	})
	if err == nil || err.Error() != "NewDB: no DSN" {
		t.Fatalf("err %v", err)
	}
}

// An injector can use a layer.Set declared in another package.
func TestSetFromAnotherPackage(t *testing.T) {
	clock, err := BuildClock(t.Context(), nil)
	if err != nil || clock.Now().IsZero() {
		t.Fatal(clock, err)
	}
}

// Decorators wrap in listing order; lazy services are built at their first
// Get, share what they need, retry a failed build, and are released before
// what was built eagerly.
func TestDecorateAndLazy(t *testing.T) {
	log, down := &Log{}, &atomic.Bool{}
	down.Store(true)
	_, err := scope.Run(t.Context(), func(s *scope.Scope) (struct{}, error) {
		r, err := BuildReports(s.Context(), s, Config{DSN: "postgres://", Log: log, SMTPDown: down})
		if err != nil {
			return struct{}{}, err
		}
		if name := r.Repo.Name(); name != "traced counted postgres" {
			t.Errorf("repo %q", name)
		}
		if slices.Contains(log.Events, "smtp dialed") {
			t.Error("smtp dialed before a Get")
		}
		if _, err := r.Mailer.Get(); err == nil || err.Error() != "NewSMTP: smtp down" {
			t.Errorf("err %v", err)
		}
		down.Store(false)
		m, err := r.Mailer.Get()
		if err != nil {
			return struct{}{}, err
		}
		p := must(r.Pager.Get())
		if m.SMTP != p.SMTP || m != must(r.Mailer.Get()) {
			t.Error("lazy services built twice")
		}
		return struct{}{}, nil
	})
	want := []string{"db opened", "smtp dialed", "smtp closed", "db closed"}
	if err != nil || !slices.Equal(log.Events, want) {
		t.Fatalf("err %v, events %q, want %q", err, log.Events, want)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
