// Package framework declares a layer.Set, as a framework would, for the
// injectors of other packages.
package framework

import (
	"time"

	"github.com/effect-go/effect-go/layer"
)

// Set is the framework's services.
var Set = layer.Set(NewClock)

// Clock tells the time.
type Clock struct{ Now func() time.Time }

// NewClock returns the system's clock.
func NewClock() *Clock { return &Clock{Now: time.Now} }
