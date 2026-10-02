package a

type Shape interface{ isShape() }

type Node interface{ node() }
type Circle struct{}
type Rect struct{}

func (Circle) isShape() {}
func (Rect) isShape()   {}

type Open interface{ Area() float64 }

func area(s Shape) {
	switch s.(type) { // want "type switch on Shape doesn't handle Rect"
	case Circle:
	}
	switch s.(type) {
	case Circle, Rect:
	}
	switch s.(type) {
	case Circle:
	default:
	}
}

func node(n Node) {
	switch n.(type) {
	case nil:
	}
}

func open(o Open) {
	switch o.(type) {
	case nil:
	}
}

type Color int

const (
	Red Color = iota
	Green
	Blue
)

func color(c Color) {
	switch c { // want "switch on Color doesn't handle Blue"
	case Red, Green:
	}
	switch c {
	case Red:
	}
	switch c {
	case Red, Green, Blue:
	}
}
