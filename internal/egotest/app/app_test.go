package app

import (
	"context"
	"slices"
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
