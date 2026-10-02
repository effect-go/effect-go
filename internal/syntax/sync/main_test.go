package main

import (
	"strings"
	"testing"
)

func TestMerge(t *testing.T) {
	base := "a\nb\nc\nd\ne\n"
	ours := "a\nb ego\nc\nd\ne\n"   // a dialect change
	theirs := "a\nb\nc\nd\ne go\n" // a Go change elsewhere
	got, n, err := merge([]byte(ours), []byte(base), []byte(theirs))
	if err != nil || n != 0 || string(got) != "a\nb ego\nc\nd\ne go\n" {
		t.Fatalf("got %q, %d conflicts, %v", got, n, err)
	}
	got, n, err = merge([]byte(ours), []byte(base), []byte("a\nb go\nc\nd\ne\n"))
	if err != nil || n != 1 || !strings.Contains(string(got), "<<<<<<<") {
		t.Fatalf("same line changed: got %q, %d conflicts, %v", got, n, err)
	}
}
