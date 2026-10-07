package bad

import "github.com/effect-go/effect-go/layer"

type DB struct{}
type Repo interface{ Get() string }
type A struct{}
type B struct{}

func (A) Get() string { return "a" }
func (B) Get() string { return "b" }

type Service struct{}
type Loop1 struct{}
type Loop2 struct{}

func NewService(db *DB) *Service     { return nil }
func NewRepoService(r Repo) *Service { return nil }
func NewA() A                        { return A{} }
func NewB() B                        { return B{} }
func NewLoop1(Loop2) Loop1           { return Loop1{} }
func NewLoop2(Loop1) Loop2           { return Loop2{} }
func Unused() *DB                    { return nil }
func NewDB() *DB                     { return &DB{} }

var Set = layer.Set(NewA, NewB)
