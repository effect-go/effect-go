package lsp

import (
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
