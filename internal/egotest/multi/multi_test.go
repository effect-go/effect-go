package multi

import "testing"

func TestMulti(t *testing.T) {
	s := NewStore[string, int]()
	n, err := Count(t.Context(), s, "a bb a ccc")
	if err != nil || n != 3 {
		t.Fatal(n, err)
	}
	if l, _ := Lookup(t.Context(), s, "ccc"); l != 3 {
		t.Fatal(l)
	}
	if l, _ := Lookup(t.Context(), s, "zz"); l != -1 {
		t.Fatal(l)
	}
}
