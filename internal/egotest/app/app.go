// Package app wires a small service with layers: a database closed when
// the app stops, a cache with a cleanup function, and a repository that
// tests swap for an in-memory one.
package app

import (
	"context"
	"errors"
	"sync"

	"github.com/effect-go/effect-go/layer"
)

// Log records what happened, in order.
type Log struct {
	mu     sync.Mutex
	Events []string
}

func (l *Log) Add(e string) {
	l.mu.Lock()
	l.Events = append(l.Events, e)
	l.mu.Unlock()
}

type Config struct {
	DSN string
	Log *Log
}

type DB struct{ log *Log }

func (db *DB) Close() error {
	db.log.Add("db closed")
	return nil
}

func NewDB(ctx context.Context, cfg Config) (*DB, error) {
	if cfg.DSN == "" {
		return nil, errors.New("no DSN")
	}
	cfg.Log.Add("db opened")
	return &DB{log: cfg.Log}, nil
}

type Cache struct{}

func NewCache(cfg Config) (*Cache, func(), error) {
	cfg.Log.Add("cache started")
	return &Cache{}, func() { cfg.Log.Add("cache stopped") }, nil
}

type Repo interface{ Name() string }

type PgRepo struct{ db *DB }

func (*PgRepo) Name() string { return "postgres" }

func NewPgRepo(db *DB) *PgRepo { return &PgRepo{db} }

type MemRepo struct{}

func (*MemRepo) Name() string { return "memory" }

func NewMemRepo() *MemRepo { return &MemRepo{} }

type Service struct {
	Repo  Repo
	Cache *Cache
}

func NewService(r Repo, c *Cache) *Service { return &Service{r, c} }

type App struct{ Service *Service }

func NewApp(s *Service) (*App, error) { return &App{s}, nil }

// AppSet is the production graph.
var AppSet = layer.Set(layer.Close(NewDB), NewCache, NewPgRepo, NewService, NewApp)
