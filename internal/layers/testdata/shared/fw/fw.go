package fw

import (
	"github.com/effect-go/effect-go/internal/layers/testdata/shared/fw/v2"
	"github.com/effect-go/effect-go/layer"
)

type Cache[T any] struct{}
type Item struct{}

func NewCache[T any]() *Cache[T] { return &Cache[T]{} }

var Set = layer.Set(lib.NewClient, NewCache[Item])
