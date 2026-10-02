package users

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

type repo map[UserID]User

func (r repo) Find(_ context.Context, id UserID) (User, bool, error) {
	if id == "broken" {
		return User{}, false, errors.New("connection refused")
	}
	u, ok := r[id]
	return u, ok, nil
}

func TestGetUser(t *testing.T) {
	h := &Handler{Users: &Service{
		Repo: repo{
			"ada":   {ID: "ada", Name: "Ada"},
			"grace": {ID: "grace", Name: "Grace", SuspendedUntil: now.Add(48 * time.Hour)},
		},
		Now: func() time.Time { return now },
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/{id}", h.GetUser)

	cases := []struct {
		id   string
		code int
		body string
	}{
		{"ada", 200, "Ada"},
		{"bob", 404, "no user bob"},
		{"grace", 403, "suspended until 2026-10-03"},
		{"broken", 503, "try again later"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/users/"+c.id, nil))
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.body) {
			t.Errorf("%s: %d %q, want %d %q", c.id, rec.Code, rec.Body.String(), c.code, c.body)
		}
	}
}

func TestStorageErrorKeepsItsCause(t *testing.T) {
	s := &Service{Repo: repo{}, Now: time.Now}
	_, err := s.Get(t.Context(), "broken")
	if _, ok := errors.AsType[Storage](err); !ok || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("err = %v", err)
	}
}

func TestEach(t *testing.T) {
	s := &Service{Repo: repo{"ada": {ID: "ada", Name: "Ada"}, "alan": {ID: "alan", Name: "Alan"}}, Now: func() time.Time { return now }}
	us, err := s.GetAll(t.Context(), []UserID{"ada", "alan"})
	if err != nil || len(us) != 2 || us[1].Name != "Alan" {
		t.Fatal(us, err)
	}
	if _, err := s.GetAll(t.Context(), []UserID{"ada", "bob"}); !errors.As(err, new(NotFound)) {
		t.Fatalf("GetAll: %v", err)
	}
	if _, err := s.GetEach(t.Context(), []UserID{"bob"}); err == nil || err.Error() != "get users: not found (ID bob)" {
		t.Fatalf("GetEach: %v", err)
	}
	if s.AllExist(t.Context(), []UserID{"ada", "alan"}) != nil || s.AllExist(t.Context(), []UserID{"zed"}) == nil {
		t.Fatal("AllExist")
	}
}
