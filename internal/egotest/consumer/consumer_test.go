package consumer

import "testing"

func TestWords(t *testing.T) {
	if n, err := Words(t.Context(), "x y x"); n != 2 || err != nil {
		t.Fatal(n, err)
	}
}
