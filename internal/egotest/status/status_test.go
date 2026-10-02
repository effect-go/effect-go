package status

import (
	"errors"
	"fmt"
	"math"
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
	if r, err := Settle(0); r != "skipped: nothing to charge" || err != nil {
		t.Fatal(r, err)
	}
	if _, err := Settle(500); err == nil || err.Error() != "settle 500: gateway: timeout" {
		t.Fatalf("Settle: %v", err)
	}
	if State(0) != "declined" || State(500) != "retry later" || State(1) != "ok" {
		t.Fatal(State(0), State(500), State(1))
	}
	if n, err := Clamp("99999999999999999999"); n != math.MaxInt || err != nil {
		t.Fatal(n, err)
	}
	if _, err := Clamp("x"); !errors.Is(err, strconv.ErrSyntax) {
		t.Fatalf("Clamp: %v", err)
	}
	if n, err := Both("1", "2"); n != 3 || err != nil {
		t.Fatal(n, err)
	}
	if _, err := Both("-5", "1"); err == nil || err.Error() != "negative" {
		t.Fatalf("Both: %v", err)
	}
	if _, err := Parse("x"); err == nil || err.Error() != `strconv.Atoi: parsing "x": invalid syntax` {
		t.Fatalf("Parse: %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("an error outside the set should panic")
		}
	}()
	Code(errors.New("other"))
}
