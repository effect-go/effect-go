package report

import (
	"slices"
	"testing"
	"time"
)

func TestSummary(t *testing.T) {
	u := &User{Address: &Address{City: "Lyon"}}
	got := Summary(u, []Order{{Total: 1}, {Total: 3, Late: true}})
	if got != "Sorry for the delay! 2 orders are on their way to Lyon." {
		t.Fatal(got)
	}
	if got := Summary(nil, nil); got != "Good news! 0 orders are on their way to your address." {
		t.Fatal(got)
	}
	if got := Summary(&User{}, nil); got != "Good news! 0 orders are on their way to your address." {
		t.Fatal(got)
	}
}

func TestShorthand(t *testing.T) {
	t.Setenv("PORT", "")
	if Lookup(map[string]int{"a": 1}, "a") != 1 || Lookup(nil, "b") != -1 {
		t.Fatal("Lookup")
	}
	if ManagerName(nil) != "nobody" || ManagerName(&User{Manager: &User{Name: "Ada"}}) != "Ada" {
		t.Fatal("ManagerName")
	}
	if Grade(95) != "A" || Grade(85) != "B" || Grade(10) != "C" {
		t.Fatal("Grade")
	}
	if got := Total([]Order{{Total: 1.5}, {Total: 2}}); got != "3.50 EUR, {braces} and 100%" {
		t.Fatal(got)
	}
	if got := Names([]*User{{Name: "a"}, nil}); !slices.Equal(got, []string{"A", "?"}) {
		t.Fatal(got)
	}
	if Describe(3) != "3 is odd" || Describe(4) != "4 is even" {
		t.Fatal("Describe")
	}
	if got := Until(time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)); got != "until 2026-10-03, quoted" {
		t.Fatal(got)
	}
	if !slices.Equal(Double([]int{1, 2}), []int{2, 4}) || Typed()(7) != "#7" {
		t.Fatal("lambdas")
	}
}
