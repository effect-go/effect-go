// Package app wires a small service with layers: a database closed when
// the app stops, a cache with a cleanup function, and a repository that
// tests swap for an in-memory one.
package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

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
	DSN      string
	Log      *Log
	SMTPDown *atomic.Bool
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

// Reports is a command: it reads the repository, and only sending a report
// needs the mail server.
type Reports struct {
	Repo   Repo
	Mailer layer.Lazy[*Mailer]
	Pager  layer.Lazy[*Pager]
}

func NewReports(r Repo, m layer.Lazy[*Mailer], p layer.Lazy[*Pager]) *Reports {
	return &Reports{r, m, p}
}

// Counted and Traced decorate a Repo.
type Counted struct{ Repo }

func (c Counted) Name() string { return "counted " + c.Repo.Name() }

func NewCounted(inner Repo) Repo { return Counted{inner} }

type Traced struct{ Repo }

func (t Traced) Name() string { return "traced " + t.Repo.Name() }

func NewTraced(inner Repo, cfg Config) Repo { return Traced{inner} }

// SMTP is the mail server both lazy services need; Down makes dialing it
// fail.
type SMTP struct{}

func NewSMTP(cfg Config) (*SMTP, func(), error) {
	if cfg.SMTPDown != nil && cfg.SMTPDown.Load() {
		return nil, nil, errors.New("smtp down")
	}
	cfg.Log.Add("smtp dialed")
	return &SMTP{}, func() { cfg.Log.Add("smtp closed") }, nil
}

type Mailer struct {
	SMTP *SMTP
	DB   *DB
}

func NewMailer(s *SMTP, db *DB) *Mailer { return &Mailer{s, db} }

type Pager struct{ SMTP *SMTP }

func NewPager(s *SMTP) *Pager { return &Pager{s} }

// ReportSet lists a decorator; the injector adds another, after it.
var ReportSet = layer.Set(layer.Close(NewDB), NewPgRepo, layer.Decorate(NewCounted), NewSMTP, NewMailer, NewPager, NewReports)
