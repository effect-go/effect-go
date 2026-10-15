package lsp

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// A corrupt Content-Length is an error, not a crash or a huge allocation.
func TestBadContentLength(t *testing.T) {
	for _, n := range []string{"-1", "x", "99999999999"} {
		c := newConn(strings.NewReader("Content-Length: "+n+"\r\n\r\n{}"), io.Discard)
		if _, err := c.read(); err == nil {
			t.Errorf("Content-Length %s: no error", n)
		}
	}
	c := newConn(strings.NewReader("Content-Length: 2\r\n\r\n{}"), io.Discard)
	if _, err := c.read(); err != nil {
		t.Fatal(err)
	}
}

// A body that isn't JSON is an error of its own: the next message can be
// read.
func TestBadMessage(t *testing.T) {
	c := newConn(strings.NewReader("Content-Length: 5\r\n\r\n{bad}Content-Length: 2\r\n\r\n{}"), io.Discard)
	if _, err := c.read(); !errors.As(err, &badMessage{}) {
		t.Fatalf("got %v, want a bad message", err)
	}
	if _, err := c.read(); err != nil {
		t.Fatal(err)
	}
}
