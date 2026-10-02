package checks

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestChecks(t *testing.T) {
	if n, err := Sum("1", "2"); n != 3 || err != nil {
		t.Fatal(n, err)
	}
	_, err := Sum("", "2")
	if !errors.Is(err, ErrEmpty) || err.Error() != "parse: empty" {
		t.Fatalf("%v", err)
	}
	_, err = Sum("1", "x")
	if err == nil || err.Error() != `parse second number x: strconv.Atoi: parsing "x": invalid syntax` {
		t.Fatalf("%v", err)
	}
	if n, err := Assign("4"); n != 4 || err != nil {
		t.Fatal(n, err)
	}
	if err := Touch(filepath.Join(t.TempDir(), "x")); err != nil {
		t.Fatal(err)
	}
	if err := Touch("/nonexistent/dir/x"); err == nil || err.Error()[:13] != "os.WriteFile:" {
		t.Fatalf("%v", err)
	}
	if OrZero("x") != 0 || OrZero("5") != 5 || OrDefault("", 9) != 9 || OrDefault("2", 9) != 2 {
		t.Fatal("else")
	}
	if MustParse("7") != 7 {
		t.Fatal("must")
	}
	if _, err := Positive(-1); err == nil || err.Error() != "negative: -1" {
		t.Fatalf("%v", err)
	}
	if _, err := Positive(0); err == nil || err.Error() != "zero" {
		t.Fatalf("%v", err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("must should panic")
		}
	}()
	MustParse("x")
}
