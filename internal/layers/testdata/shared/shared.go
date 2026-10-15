package shared

import (
	"context"

	"github.com/effect-go/effect-go/internal/layers/testdata/shared/fw"
	"github.com/effect-go/effect-go/internal/layers/testdata/shared/lib"
	"github.com/effect-go/effect-go/layer"
)

type Getter interface{ Get() string }
type Putter interface{ Put(string) }

type Store struct{}

func (*Store) Get() string { return "" }
func (*Store) Put(string)  {}

type A struct{}
type B struct{}
type App struct{}

func NewStore() *Store               { return &Store{} }
func NewA(g Getter) A                { return A{} }
func NewB(p Putter, g Getter) B      { return B{} }
func NewApp(a A, b B, s *Store) *App { return &App{} }

// Two sets with NewDB, whose cleanup has a type of its own.
type DB struct{}
type Closer func(context.Context) error
type Repo struct{}
type Mailer struct{}
type Service struct{}

func NewDB() (*DB, Closer, error)        { return &DB{}, nil, nil }
func NewRepo(*DB) *Repo                  { return &Repo{} }
func NewMailer(*DB) *Mailer              { return &Mailer{} }
func NewService(*Repo, *Mailer) *Service { return &Service{} }

var (
	RepoSet = layer.Set(NewDB, NewRepo)
	MailSet = layer.Set(NewDB, NewMailer)
)

// A set that provides an interface, which a test graph swaps.
type Mem struct{}

func (*Mem) Get() string { return "" }
func NewGetter() Getter  { return &Store{} }
func NewMem() *Mem       { return &Mem{} }

var GetterSet = layer.Set(NewGetter, NewA)

// A variadic provider.
type Option func(*Server)
type Server struct{}

func Defaults() []Option               { return nil }
func NewServer(opts ...Option) *Server { return &Server{} }

// fw.Set provides a type from a package named lib at fw/v2, and an
// instance of a generic function.
type Pinger interface{ Ping() }
type Remote struct{}

func NewRemote(p Pinger, c *fw.Cache[fw.Item], cfg lib.Config) *Remote { return &Remote{} }
