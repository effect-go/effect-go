// Package users is a user service and its HTTP handler, written in plain Go
// with the effectgo runtime. It is the baseline for the dialect: users.ego is
// the same code in EffectGo, which ego generate will compile to Go like this.
package users

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"effectgo/trace"
)

type UserID string

type User struct {
	ID             UserID
	Name           string
	SuspendedUntil time.Time
}

// Repo stores users. Find reports a missing user as found == false, not as
// an error: whether that's a failure is the service's decision.
type Repo interface {
	Find(ctx context.Context, id UserID) (u User, found bool, err error)
}

// UserError is a closed set of errors: NotFound | Suspended | Storage.
type UserError interface {
	error
	isUserError()
}

type NotFound struct{ ID UserID }

func (NotFound) isUserError()    {}
func (e NotFound) Error() string { return fmt.Sprintf("user %s not found", e.ID) }

type Suspended struct{ Until time.Time }

func (Suspended) isUserError()    {}
func (e Suspended) Error() string { return fmt.Sprintf("user suspended until %v", e.Until) }

type Storage struct{ Cause error }

func (Storage) isUserError()    {}
func (e Storage) Error() string { return "user storage: " + e.Cause.Error() }
func (e Storage) Unwrap() error { return e.Cause }

type Service struct {
	Repo Repo
	Now  func() time.Time
}

// Get fails with a UserError: NotFound | Suspended | Storage.
func (s *Service) Get(ctx context.Context, id UserID) (_ User, err error) {
	ctx, span := trace.Start(ctx, "Service.Get")
	defer trace.End(span, &err)
	u, ok, err := s.Repo.Find(ctx, id)
	if err != nil {
		return User{}, Storage{Cause: err}
	}
	if !ok {
		return User{}, NotFound{ID: id}
	}
	if u.SuspendedUntil.After(s.Now()) {
		return User{}, Suspended{Until: u.SuspendedUntil}
	}
	return u, nil
}

type Handler struct{ Users *Service }

// GetUser serves GET /users/{id}. Go doesn't check that every UserError case
// is handled here: a new case silently falls through to the 500.
func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	u, err := h.Users.Get(r.Context(), UserID(r.PathValue("id")))
	if err == nil {
		fmt.Fprintf(w, "%s\n", u.Name)
	} else if e, ok := errors.AsType[NotFound](err); ok {
		http.Error(w, fmt.Sprintf("no user %s", e.ID), http.StatusNotFound)
	} else if e, ok := errors.AsType[Suspended](err); ok {
		http.Error(w, fmt.Sprintf("suspended until %v", e.Until.Format(time.DateOnly)), http.StatusForbidden)
	} else if _, ok := errors.AsType[Storage](err); ok {
		http.Error(w, "try again later", http.StatusServiceUnavailable)
	} else {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
