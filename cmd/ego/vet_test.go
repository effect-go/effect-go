package main

import (
	"slices"
	"testing"
)

// The analyzers' flags only go to the run with the analyzers: go vet's own
// doesn't know them.
func TestVetArgs(t *testing.T) {
	got := vetArgs([]string{"-exhaustive", "-v", "-ctxcheck.strict=true", "--fiberjoin=false", "-printf", "./..."})
	if want := []string{"-v", "-printf", "./..."}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
