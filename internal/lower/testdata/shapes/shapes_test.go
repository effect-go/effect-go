package shapes

import "testing"

func TestShapes(t *testing.T) {
	if Area(Rect{W: 2, H: 3}) != 6 || Area(Dot{}) != 0 || Area(Circle{R: 1}) < 3.14 {
		t.Fatal("Area")
	}
	if !Warm(Red) || Warm(Blue) || Green.String() != "Green" || Color(7).String() != "Color(7)" {
		t.Fatal("Color")
	}
	if Name(High) != "high" || Name(Level(9)) != "?" {
		t.Fatal("Name")
	}
}
