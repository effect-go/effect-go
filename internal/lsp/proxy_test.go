package lsp

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// client is a minimal LSP client, playing the editor.
type client struct {
	t      *testing.T
	c      *conn
	mu     sync.Mutex
	nextID int
	waits  map[string]chan *message
	diags  chan *message
}

func gopls(t *testing.T) string {
	if p := os.Getenv("EGO_GOPLS"); p != "" {
		return p
	}
	p, err := exec.LookPath("gopls")
	if err != nil {
		t.Skip("gopls not found: set EGO_GOPLS")
	}
	return p
}

func start(t *testing.T) (*client, string) {
	if testing.Short() {
		t.Skip("starts gopls")
	}
	root, _ := filepath.Abs("../..")
	cin, sout := io.Pipe()
	sin, cout := io.Pipe()
	go func() {
		var log io.Writer = io.Discard
		if f := os.Getenv("EGO_LSP_LOG"); f != "" {
			log, _ = os.Create(f)
		}
		if err := Run(Config{Gopls: gopls(t), Log: log}, sin, sout); err != nil {
			t.Log("proxy:", err)
		}
	}()
	cl := &client{t: t, c: newConn(cin, cout), waits: map[string]chan *message{}, diags: make(chan *message, 100)}
	go cl.loop()
	t.Cleanup(func() {
		cl.call("shutdown", nil)
		cl.c.notify("exit", nil)
		cout.Close()
	})
	cl.call("initialize", map[string]any{
		"processId":        nil,
		"rootUri":          pathToURI(root),
		"capabilities":     map[string]any{},
		"workspaceFolders": []any{map[string]any{"uri": pathToURI(root), "name": "effect-go"}},
	})
	cl.c.notify("initialized", map[string]any{})
	return cl, root
}

func (cl *client) loop() {
	for {
		m, err := cl.c.read()
		if err != nil {
			return
		}
		switch {
		case m.isResponse():
			cl.mu.Lock()
			ch := cl.waits[string(m.ID)]
			cl.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		case m.isRequest():
			var res any
			if m.Method == "workspace/configuration" {
				var p struct{ Items []any }
				json.Unmarshal(m.Params, &p)
				res = make([]any, len(p.Items))
			}
			cl.c.reply(m.ID, res)
		case m.Method == "textDocument/publishDiagnostics":
			cl.diags <- m
		}
	}
}

func (cl *client) call(method string, params any) json.RawMessage {
	cl.mu.Lock()
	cl.nextID++
	id := json.RawMessage(fmt.Sprint(cl.nextID))
	ch := make(chan *message, 1)
	cl.waits[string(id)] = ch
	cl.mu.Unlock()
	p, _ := json.Marshal(params)
	cl.c.write(&message{ID: id, Method: method, Params: p})
	select {
	case m := <-ch:
		if len(m.Error) > 0 {
			cl.t.Fatalf("%s: %s", method, m.Error)
		}
		return m.Result
	case <-time.After(60 * time.Second):
		cl.t.Fatalf("%s: no response", method)
	}
	return nil
}

// waitDiags waits for diagnostics of uri that satisfy ok.
func (cl *client) waitDiags(uri string, ok func([]map[string]any) bool) []map[string]any {
	deadline := time.After(60 * time.Second)
	for {
		select {
		case m := <-cl.diags:
			var p struct {
				URI         string
				Diagnostics []map[string]any
			}
			json.Unmarshal(m.Params, &p)
			if p.URI == uri && ok(p.Diagnostics) {
				return p.Diagnostics
			}
		case <-deadline:
			cl.t.Fatalf("no matching diagnostics for %s", uri)
		}
	}
}

// at returns the position of the n-th occurrence of needle in src, plus
// delta characters.
func at(src, needle string, delta int) Position {
	i := strings.Index(src, needle)
	if i < 0 {
		panic(needle)
	}
	return newText([]byte(src)).position(i + delta)
}

func TestEditor(t *testing.T) {
	cl, root := start(t)
	path := filepath.Join(root, "internal/egotest/users/users.ego")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	uri := pathToURI(path)
	cl.c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "ego", "version": 1, "text": src}})

	t.Run("no diagnostics", func(t *testing.T) {
		cl.waitDiags(uri, func(ds []map[string]any) bool { return len(ds) == 0 })
	})

	t.Run("hover", func(t *testing.T) {
		res := cl.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at(src, "Repo.Find(id)", 6)})
		if !strings.Contains(string(res), "Find(ctx context.Context, id UserID)") {
			t.Fatalf("hover: %s", res)
		}
	})

	t.Run("definition", func(t *testing.T) {
		for _, c := range []struct {
			from  string
			delta int
			to    string
		}{
			{"NotFound(e)", 1, "NotFound{ ID UserID }"},
			{"Users.Get(r", 6, "Get(id UserID) (User, UserError)"},
			{"Storage(_)", 1, "Storage{ Cause error }"},
			{"s.Repo.Find", 7, "Find(id UserID)"},
		} {
			res := cl.call("textDocument/definition", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at(src, c.from, c.delta)})
			var locs []struct {
				URI   string
				Range Range
			}
			json.Unmarshal(res, &locs)
			want := at(src, c.to, 0)
			if len(locs) == 0 || locs[0].URI != uri || locs[0].Range.Start.Line != want.Line {
				t.Errorf("definition of %s: %s, want line %d", c.from, res, want.Line)
			}
		}
	})

	t.Run("gopls diagnostics", func(t *testing.T) {
		bad := strings.Replace(src, "u.Name)", "u.Nam)", 1)
		cl.c.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": 2}, "contentChanges": []any{map[string]any{"text": bad}}})
		ds := cl.waitDiags(uri, func(ds []map[string]any) bool { return len(ds) > 0 })
		b, _ := json.Marshal(ds[0])
		want := at(bad, "u.Nam)", 0)
		if !strings.Contains(string(b), "Nam") || !strings.Contains(string(b), fmt.Sprintf(`"line":%d`, want.Line)) {
			t.Fatalf("diagnostic %s, want line %d", b, want.Line)
		}
	})

	t.Run("compiler diagnostics", func(t *testing.T) {
		bad := strings.Replace(src, "		Storage(_)   => http.Error(w, \"try again later\", http.StatusServiceUnavailable)\n", "", 1)
		cl.c.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": 3}, "contentChanges": []any{map[string]any{"text": bad}}})
		ds := cl.waitDiags(uri, func(ds []map[string]any) bool {
			b, _ := json.Marshal(ds)
			return strings.Contains(string(b), "doesn't handle Storage")
		})
		_ = ds
		cl.c.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": 4}, "contentChanges": []any{map[string]any{"text": src}}})
		cl.waitDiags(uri, func(ds []map[string]any) bool { return len(ds) == 0 })
	})

	t.Run("format", func(t *testing.T) {
		ugly := strings.Replace(src, "	u, ok := check s.Repo.Find(id) as Storage", "u,ok:=check   s.Repo.Find(id)   as Storage", 1)
		cl.c.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": 5}, "contentChanges": []any{map[string]any{"text": ugly}}})
		res := cl.call("textDocument/formatting", map[string]any{"textDocument": map[string]any{"uri": uri}, "options": map[string]any{"tabSize": 4, "insertSpaces": false}})
		var edits []struct{ NewText string }
		json.Unmarshal(res, &edits)
		if len(edits) != 1 || edits[0].NewText != src {
			t.Fatalf("format: %s", res)
		}
		cl.c.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": 6}, "contentChanges": []any{map[string]any{"text": src}}})
	})

	t.Run("rename", func(t *testing.T) {
		res := cl.call("textDocument/rename", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at(src, "Now  func", 0), "newName": "Clock"})
		var we struct {
			Changes         map[string][]struct{ Range Range }
			DocumentChanges []struct {
				TextDocument struct{ URI string }
				Edits        []struct {
					Range   Range
					NewText string
				}
			}
		}
		json.Unmarshal(res, &we)
		n := 0
		for _, dc := range we.DocumentChanges {
			if dc.TextDocument.URI == uri {
				for _, e := range dc.Edits {
					if e.NewText == "Clock" {
						n++
					}
				}
			}
		}
		n += len(we.Changes[uri])
		if n != 2 { // the field and s.Now()
			t.Fatalf("rename: %d edits in users.ego: %s", n, res)
		}
	})
}
