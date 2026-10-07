package shared

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
