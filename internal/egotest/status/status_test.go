package status

import (
	"errors"
	"fmt"
	"strconv"
	"testing"
)

func TestStatus(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{nil, 200},
		{Declined{Reason: "x"}, 402},
		{fmt.Errorf("wrapped: %w", Expired{}), 410},
		{Gateway{Cause: errors.New("io")}, 502},
	}
	for _, c := range cases {
		if got := Code(c.err); got != c.code {
			t.Errorf("Code(%v) = %d, want %d", c.err, got, c.code)
		}
	}
	if Describe(Declined{Reason: "card"}) != "card" || Describe(Expired{}) != "failed: expired" {
		t.Fatal(Describe(Expired{}))
	}
	if d := (Declined{Reason: "card"}); d.Error() != "declined: card" {
		t.Fatal(d.Error())
	}
	_, err := Pay(500)
	if _, ok := errors.AsType[Gateway](err); !ok || err.Error() != "gateway: timeout" {
		t.Fatalf("err %v", err)
	}
	if _, err := Amount("x"); err == nil || err.Error() != `declined: bad amount "x"` {
		t.Fatalf("Amount: %v", err)
	}
	if _, err := Remote("x"); !errors.Is(err, strconv.ErrSyntax) {
		t.Fatalf("Remote: %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("an error outside the set should panic")
		}
	}()
	Code(errors.New("other"))
}
