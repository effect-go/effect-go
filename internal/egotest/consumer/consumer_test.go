package consumer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/effect-go/effect-go/internal/egotest/users"
)

func TestWords(t *testing.T) {
	if n, err := Words(t.Context(), "x y x"); n != 2 || err != nil {
		t.Fatal(n, err)
	}
}

type repo map[users.UserID]users.User

func (r repo) Find(_ context.Context, id users.UserID) (users.User, bool, error) {
	if id == "broken" {
		return users.User{}, false, errors.New("disk")
	}
	u, ok := r[id]
	return u, ok, nil
}

func TestStatus(t *testing.T) {
	s := &users.Service{Repo: repo{"ada": {ID: "ada"}}, Now: time.Now}
	for id, want := range map[users.UserID]int{"ada": 200, "bob": 404, "broken": 503} {
		if got := Status(t.Context(), s, id); got != want {
			t.Errorf("Status(%s) = %d, want %d", id, got, want)
		}
	}
}
