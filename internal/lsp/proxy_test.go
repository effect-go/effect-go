package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/effect-go/effect-go/internal/lower"
)

// client is a minimal LSP client, playing the editor.
type client struct {
	t      *testing.T
	c      *conn
	mu     sync.Mutex
	nextID int
	waits  map[string]chan *message
	diags  chan *message
	bad    chan *message // error replies to no request
	done   chan struct{} // closed when the proxy ends
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

// start starts the proxy, and gopls, on the workspace root, the
// repository's if "".
func start(t *testing.T, root string) (*client, string) {
	if testing.Short() {
		t.Skip("starts gopls")
	}
	if root == "" {
		root, _ = filepath.Abs("../..")
	}
	cfg := Config{Gopls: gopls(t), Log: io.Discard} // here: Skip must run on the test goroutine
	if f := os.Getenv("EGO_LSP_LOG"); f != "" {
		cfg.Log, _ = os.Create(f)
	}
	cin, sout := io.Pipe()
	sin, cout := io.Pipe()
	cl := &client{t: t, c: newConn(cin, cout), waits: map[string]chan *message{}, diags: make(chan *message, 100), bad: make(chan *message, 1), done: make(chan struct{})}
	go func() {
		if err := Run(cfg, sin, sout); err != nil {
			t.Log("proxy:", err)
		}
		// So that the client doesn't wait for it: the cleanup waits for
		// this.
		sin.Close()
		sout.Close()
	}()
	go cl.loop()
	t.Cleanup(func() {
		cl.callErr("shutdown", nil)
		cl.c.notify("exit", nil)
		cout.Close()
		<-cl.done
	})
	cl.call("initialize", map[string]any{
		"processId": nil,
		"rootUri":   pathToURI(root),
		"capabilities": map[string]any{
			"workspace": map[string]any{"workspaceEdit": map[string]any{"documentChanges": true}},
			// The proxy counts in UTF-16: gopls mustn't pick UTF-8.
			"general": map[string]any{"positionEncodings": []string{"utf-8", "utf-16"}},
		},
		"workspaceFolders": []any{map[string]any{"uri": pathToURI(root), "name": "effect-go"}},
	})
	cl.c.notify("initialized", map[string]any{})
	return cl, root
}

func (cl *client) loop() {
	defer close(cl.done)
	for {
		m, err := cl.c.read()
		if err != nil {
			return
		}
		switch {
		case string(m.ID) == "null":
			cl.bad <- m
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
	res, err := cl.callErr(method, params)
	if err != nil {
		cl.t.Fatalf("%s: %s", method, err)
	}
	return res
}

// callErr is call, for a request that may fail: it returns the error.
func (cl *client) callErr(method string, params any) (json.RawMessage, json.RawMessage) {
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
		return m.Result, m.Error
	case <-cl.done:
		cl.t.Fatalf("%s: the proxy ended", method)
	case <-time.After(60 * time.Second):
		cl.t.Fatalf("%s: no response", method)
	}
	return nil, nil
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
		case <-cl.done:
			cl.t.Fatal("the proxy ended")
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

// writeFiles writes files, by their paths in dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, src := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o666); err != nil {
			t.Fatal(err)
		}
	}
}

// generate writes the generated Go of the package in dir.
func generate(t *testing.T, dir string) {
	t.Helper()
	res, err := lower.Generate(lower.Config{Dir: dir})
	if err == nil {
		err = res.Err()
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range res.Outputs {
		if err := os.WriteFile(out.Go, out.Code, 0o666); err != nil {
			t.Fatal(err)
		}
	}
}

// editorModule writes a module for TestEditor, in a directory whose name
// editors escape in URIs: the users package, with a .go file using it, a
// file left from a renamed .ego file, and a .ego package using it.
func editorModule(t *testing.T) (root, users string) {
	repo, _ := filepath.Abs("../..")
	src, err := os.ReadFile(filepath.Join(repo, "internal/egotest/users/users.ego"))
	if err != nil {
		t.Fatal(err)
	}
	// Characters that take more UTF-16 code units than UTF-8 bytes, on a
	// line the test hovers on.
	ego := strings.Replace(string(src), `"try again later"`, `"🎉🎉🎉 later"`, 1)
	root = filepath.Join(t.TempDir(), "at@dir")
	writeFiles(t, root, map[string]string{
		"go.mod":          "module example.com/tmp\n\ngo 1.26.0\n",
		"go.work":         "go 1.26.0\n\nuse (\n\t.\n\t" + repo + "\n)\n",
		"users/users.ego": ego,
		"users/later.go":  "package users\n\nimport \"time\"\n\n// Later is plain Go next to the .ego file.\nfunc (s *Service) Later() time.Time { return s.Now().Add(time.Hour) }\n",
		"app/app.ego":     "package app\n\nimport \"example.com/tmp/users\"\n\neffect All(s *users.Service, ids []users.UserID) ([]users.User, error) {\n\treturn s.GetAll(ids)\n}\n",
	})
	users = filepath.Join(root, "users")
	generate(t, users)
	generate(t, filepath.Join(root, "app"))
	writeFiles(t, users, map[string]string{
		"old_ego.go": "// Code generated by ego generate from old.ego. DO NOT EDIT.\n\npackage users\n\ntype UserID string\n",
	})
	return root, users
}

func TestEditor(t *testing.T) {
	root, dir := editorModule(t)
	cl, _ := start(t, root)
	path := filepath.Join(dir, "users.ego")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	uri := strings.Replace(pathToURI(path), "@", "%40", 1) // as VS Code writes it
	cl.c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "ego", "version": 1, "text": src}})

	t.Run("no diagnostics", func(t *testing.T) {
		cl.waitDiags(uri, func(ds []map[string]any) bool { return len(ds) == 0 })
	})

	t.Run("hover", func(t *testing.T) {
		for _, c := range []struct {
			at    string
			delta int
			want  string
		}{
			{"Repo.Find(id)", 6, "Find(ctx context.Context, id UserID)"},
			{"http.StatusServiceUnavailable", 5, "StatusServiceUnavailable"},
		} {
			res := cl.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at(src, c.at, c.delta)})
			if !strings.Contains(string(res), c.want) {
				t.Errorf("hover at %s: %s", c.at, res)
			}
		}
	})

	t.Run("bad requests", func(t *testing.T) {
		// A position off the document gets an answer, and the session
		// goes on.
		if _, err := cl.callErr("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": Position{Line: -1, Character: -1}}); err != nil {
			t.Logf("line -1: %s", err)
		}
		// So does a message that isn't JSON.
		cl.c.mu.Lock()
		io.WriteString(cl.c.w, "Content-Length: 5\r\n\r\n{bad}")
		cl.c.mu.Unlock()
		select {
		case m := <-cl.bad:
			if !strings.Contains(string(m.Error), "-32700") {
				t.Errorf("reply: %s", m.Error)
			}
		case <-time.After(10 * time.Second):
			t.Error("no reply to a message that isn't JSON")
		}
		res := cl.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at(src, "Repo.Find(id)", 6)})
		if !strings.Contains(string(res), "Find(ctx") {
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
			{"Users.Get(UserID", 6, "Get(id UserID) (User, UserError)"},
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
		b, _ := json.Marshal(ds)
		want := at(bad, "u.Nam)", 0)
		// Only this one: none from old_ego.go, which declares UserID again.
		if len(ds) != 1 || !strings.Contains(string(b), "Nam") || !strings.Contains(string(b), fmt.Sprintf(`"line":%d`, want.Line)) {
			t.Fatalf("diagnostics %s, want one on line %d", b, want.Line)
		}
	})

	t.Run("compiler diagnostics", func(t *testing.T) {
		bad := strings.Replace(src, "		Storage(_)   => http.Error(w, \"🎉🎉🎉 later\", http.StatusServiceUnavailable)\n", "", 1)
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

	t.Run("completion", func(t *testing.T) {
		res := cl.call("textDocument/completion", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at(src, "s.Repo.Find", len("s.Repo."))})
		if !strings.Contains(string(res), `"label":"Find"`) {
			t.Fatalf("completion: %.300s", res)
		}
	})

	t.Run("completion with an import", func(t *testing.T) {
		// The generated Go imports more than users.ego: the import goes in
		// users.ego's own block.
		typed := strings.Replace(src, "\treturn u.Name, nil", "\t_ = strings.ToU\n\treturn u.Name, nil", 1)
		cl.c.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": 7}, "contentChanges": []any{map[string]any{"text": typed}}})
		res := cl.call("textDocument/completion", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at(typed, "strings.ToU", len("strings.ToU"))})
		var list struct {
			Items []struct {
				Label               string
				AdditionalTextEdits []any
			}
		}
		json.Unmarshal(res, &list)
		found := false
		for _, it := range list.Items {
			if it.Label != "ToUpper" {
				continue
			}
			found = true
			got := string(applyEdits([]byte(typed), it.AdditionalTextEdits))
			if want := strings.Replace(typed, "\t\"time\"\n)", "\t\"time\"\n\t\"strings\"\n)", 1); got != want {
				t.Errorf("edits %v give\n%.200s", it.AdditionalTextEdits, got)
			}
		}
		if !found {
			t.Errorf("no ToUpper: %.300s", res)
		}
		cl.c.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": 8}, "contentChanges": []any{map[string]any{"text": src}}})
	})

	t.Run("save", func(t *testing.T) {
		gen := filepath.Join(filepath.Dir(path), "users_ego.go")
		before, _ := os.ReadFile(gen)
		cl.c.notify("textDocument/didSave", map[string]any{"textDocument": map[string]any{"uri": uri}})
		cl.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at(src, "Repo.Find", 6)})
		if after, _ := os.ReadFile(gen); string(after) != string(before) {
			t.Fatal("saving an unchanged file rewrote its generated code")
		}
	})

	t.Run("rename", func(t *testing.T) {
		res := cl.call("textDocument/rename", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at(src, "Now  func", 0), "newName": "Clock"})
		var we struct {
			DocumentChanges []struct {
				TextDocument struct{ URI string }
				Edits        []struct {
					Range   Range
					NewText string
				}
			}
		}
		json.Unmarshal(res, &we)
		later := filepath.Join(dir, "later.go")
		laterSrc, _ := os.ReadFile(later)
		want := map[string][]Range{
			uri: {{at(src, "Now  func", 0), at(src, "Now  func", 3)}, {at(src, "s.Now()", 2), at(src, "s.Now()", 5)}},
			// Go files' edits stay as gopls gives them.
			pathToURI(later): {{at(string(laterSrc), "s.Now()", 2), at(string(laterSrc), "s.Now()", 5)}},
		}
		got := map[string][]Range{}
		for _, dc := range we.DocumentChanges {
			for _, e := range dc.Edits {
				if e.NewText == "Clock" {
					got[dc.TextDocument.URI] = append(got[dc.TextDocument.URI], e.Range)
				}
			}
		}
		for _, rs := range got {
			sort.Slice(rs, func(i, j int) bool { return rs[i].Start.Line < rs[j].Start.Line })
		}
		for u, rs := range want {
			if fmt.Sprint(got[u]) != fmt.Sprint(rs) {
				t.Errorf("rename: edits in %s: %v, want %v\n%s", filepath.Base(u), got[u], rs, res)
			}
		}
	})

	t.Run("rename in a .ego file not open", func(t *testing.T) {
		// app.ego calls GetAll: its edits can't be translated without its
		// source map.
		_, err := cl.callErr("textDocument/rename", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at(src, "GetAll(ids", 0), "newName": "All"})
		if !strings.Contains(string(err), "app/app.ego") {
			t.Fatalf("rename: %s", err)
		}
	})

	t.Run("close", func(t *testing.T) {
		cl.c.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": uri}})
		cl.waitDiags(uri, func(ds []map[string]any) bool { return len(ds) == 0 })
		res := cl.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at(src, "Repo.Find", 6)})
		if string(res) != "null" {
			t.Fatalf("hover after close: %s", res)
		}
	})
}

// The buffers that a directory's files have in gopls are closed with the
// directory's last .ego file, those of .ego files the editor doesn't have
// open too, and follow these files on disk until then. A file left from a
// renamed .ego file has an empty buffer, so that gopls doesn't see its
// declarations twice.
func TestCloseDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":     "module example.com/c\n\ngo 1.26\n",
		"a.ego":      "package c\n\nfunc A() int { return 1 }\n",
		"b.ego":      "package c\n\nfunc B() int { return 2 }\n",
		"old_ego.go": "// Code generated by ego generate from old.ego. DO NOT EDIT.\n\npackage c\n\nfunc A() int { return 1 }\n",
	})
	var sent bytes.Buffer // to gopls
	p := newProxy(Config{}, newConn(strings.NewReader(""), io.Discard), newConn(strings.NewReader(""), &sent))
	notify := func(method string, params any) {
		t.Helper()
		b, _ := json.Marshal(params)
		if err := p.handleEditor(&message{Method: method, Params: b}); err != nil {
			t.Fatal(err)
		}
	}
	doc := func(name string) any {
		path := filepath.Join(dir, name)
		text, _ := os.ReadFile(path)
		return map[string]any{"textDocument": map[string]any{"uri": pathToURI(path), "text": string(text)}}
	}
	// expect checks the methods and files of what gopls received since
	// the last call, and returns the files' new texts.
	expect := func(want string) map[string]string {
		t.Helper()
		got, texts := received(&sent)
		if got != want {
			t.Errorf("gopls received %q, want %q", got, want)
		}
		return texts
	}
	const closed = "didClose a_ego.go, didClose b_ego.go, didClose old_ego.go"

	notify("textDocument/didOpen", doc("a.ego"))
	if texts := expect("didOpen a_ego.go, didOpen b_ego.go, didOpen old_ego.go"); texts["old_ego.go"] != "package c\n" {
		t.Errorf("old_ego.go: %q", texts["old_ego.go"])
	}
	notify("textDocument/didClose", doc("a.ego"))
	expect(closed)

	notify("textDocument/didOpen", doc("a.ego"))
	notify("textDocument/didOpen", doc("b.ego"))
	expect("didOpen a_ego.go, didOpen b_ego.go, didOpen old_ego.go")
	notify("textDocument/didClose", doc("a.ego"))
	expect("")

	writeFiles(t, dir, map[string]string{"a.ego": "package c\n\nfunc A() int { return 3 }\n"})
	notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []any{map[string]any{"uri": pathToURI(filepath.Join(dir, "a.ego")), "type": 2}}})
	if err := p.flush(dir); err != nil {
		t.Fatal(err)
	}
	if texts := expect("didChange a_ego.go, workspace/didChangeWatchedFiles"); !strings.Contains(texts["a_ego.go"], "return 3") {
		t.Errorf("a_ego.go: %q", texts["a_ego.go"])
	}

	notify("textDocument/didClose", doc("b.ego"))
	expect(closed)
}

// An open file that build constraints exclude here has a buffer in gopls,
// closed with the file even while others of its directory stay open.
func TestCloseExcluded(t *testing.T) {
	other := "linux"
	if runtime.GOOS == other {
		other = "darwin"
	}
	dir := t.TempDir()
	name := "x_" + other + ".ego"
	writeFiles(t, dir, map[string]string{
		"go.mod": "module example.com/c\n\ngo 1.26\n",
		"a.ego":  "package c\n\nfunc A() int { return 1 }\n",
		name:     "package c\n\nfunc X() int { return A() }\n",
	})
	var sent bytes.Buffer // to gopls
	p := newProxy(Config{}, newConn(strings.NewReader(""), io.Discard), newConn(strings.NewReader(""), &sent))
	notify := func(method, name string) {
		t.Helper()
		path := filepath.Join(dir, name)
		text, _ := os.ReadFile(path)
		b, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": pathToURI(path), "text": string(text)}})
		if err := p.handleEditor(&message{Method: method, Params: b}); err != nil {
			t.Fatal(err)
		}
	}
	gen := "x_" + other + "_ego.go"
	notify("textDocument/didOpen", "a.ego")
	notify("textDocument/didOpen", name)
	if got, _ := received(&sent); got != "didOpen a_ego.go, didOpen "+gen {
		t.Errorf("gopls received %q", got)
	}
	notify("textDocument/didClose", name)
	if got, _ := received(&sent); got != "didClose "+gen {
		t.Errorf("gopls received %q, want its buffer closed", got)
	}
}

// received returns the methods and files of the messages in sent, which
// gopls received, sorted, and the files' new texts. It empties sent.
func received(sent *bytes.Buffer) (string, map[string]string) {
	c := newConn(bytes.NewReader(sent.Bytes()), nil)
	sent.Reset()
	var got []string
	texts := map[string]string{}
	for m, err := c.read(); err == nil; m, err = c.read() {
		var dp docParams
		json.Unmarshal(m.Params, &dp)
		if dp.TextDocument.URI == "" {
			got = append(got, m.Method)
			continue
		}
		name := filepath.Base(uriToPath(dp.TextDocument.URI))
		got = append(got, strings.TrimPrefix(m.Method, "textDocument/")+" "+name)
		texts[name] = dp.TextDocument.Text
		for _, c := range dp.ContentChanges {
			texts[name] = c.Text
		}
	}
	sort.Strings(got)
	return strings.Join(got, ", "), texts
}

// Saving a .ego file removes the files generated from .ego files that are
// gone, as ego generate does, a case-only rename's too, and the wiring once
// no injector is left; hand-written files stay.
func TestSaveLeftovers(t *testing.T) {
	dir := t.TempDir()
	const header = "// Code generated by ego generate from %s. DO NOT EDIT.\n\npackage c\n"
	writeFiles(t, dir, map[string]string{
		"go.mod":        "module example.com/c\n\ngo 1.26\n",
		"a.ego":         "package c\n\nfunc A() int { return 1 }\n",
		"A_ego.go":      fmt.Sprintf(header, "A.ego"), // A.ego was renamed a.ego
		"old_ego.go":    fmt.Sprintf(header, "old.ego"),
		"hand_ego.go":   "package c\n\nfunc H() {}\n",
		"inject.go":     "//go:build egolayers\n\npackage c\n",
		"layers_ego.go": "// Code generated by ego generate. DO NOT EDIT.\n\npackage c\n",
	})
	var sent bytes.Buffer // to gopls
	p := newProxy(Config{}, newConn(strings.NewReader(""), io.Discard), newConn(strings.NewReader(""), &sent))
	uri := pathToURI(filepath.Join(dir, "a.ego"))
	notify := func(method string, params any) {
		t.Helper()
		b, _ := json.Marshal(params)
		if err := p.handleEditor(&message{Method: method, Params: b}); err != nil {
			t.Fatal(err)
		}
	}
	files := func() string {
		t.Helper()
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".go") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		return strings.Join(names, " ")
	}
	notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "text": "package c\n\nfunc A() int { return 1 }\n"}})
	received(&sent)

	notify("textDocument/didSave", map[string]any{"textDocument": map[string]any{"uri": uri}})
	if got, want := files(), "a_ego.go hand_ego.go inject.go layers_ego.go"; got != want {
		t.Errorf("after saving: %s, want %s", got, want)
	}
	// Their empty buffers too.
	if got, _ := received(&sent); got != "didClose A_ego.go, didClose old_ego.go" {
		t.Errorf("gopls received %q", got)
	}

	if err := os.Remove(filepath.Join(dir, "inject.go")); err != nil {
		t.Fatal(err)
	}
	notify("textDocument/didSave", map[string]any{"textDocument": map[string]any{"uri": uri}})
	if got, want := files(), "a_ego.go hand_ego.go"; got != want {
		t.Errorf("without injectors: %s, want %s", got, want)
	}
}

// A position in a generated file that gopls reads from disk, formatted,
// maps to the .ego file, with the //line directives or without; one in a
// hand-written file, or in generated Go that is stale, doesn't.
func TestDiskDocs(t *testing.T) {
	_, dir := editorModule(t)
	p := newProxy(Config{}, newConn(strings.NewReader(""), io.Discard), newConn(strings.NewReader(""), io.Discard))
	goPath, egoPath := filepath.Join(dir, "users_ego.go"), filepath.Join(dir, "users.ego")
	goURI := pathToURI(goPath)
	read := func(path string) string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// check maps every identifier of the generated file that maps exactly,
	// and some declarations.
	check := func() {
		t.Helper()
		doc := p.docOf(goURI)
		if doc == nil {
			t.Fatal("no document")
		}
		gen, src := read(goPath), read(egoPath)
		gt, st := newText([]byte(gen)), newText([]byte(src))
		exact := 0
		for _, tk := range tokens([]byte(gen)) {
			if !token.IsIdentifier(tk.key) {
				continue
			}
			r, ok := p.mapRange(Range{gt.position(tk.off), gt.position(tk.off + tk.n)}, doc)
			if !ok {
				continue
			}
			exact++
			m := r.(Range)
			if got := src[st.offset(m.Start):st.offset(m.End)]; got != tk.key {
				t.Errorf("%s at %v maps to %q at %v", tk.key, gt.position(tk.off), got, m.Start)
			}
		}
		if exact < 100 {
			t.Errorf("only %d identifiers map exactly", exact)
		}
		for _, c := range []struct {
			gen, src string
			delta    int
		}{
			{"Get(ctx context.Context, id UserID)", "Get(id UserID)", 0},
			{"NotFound struct", "NotFound{ ID", 0},
			{"Service struct", "Service struct", 0},
			{"GetAll(ctx", "GetAll(ids", 0},
			{"http.StatusServiceUnavailable", "http.StatusServiceUnavailable", 5}, // after 🎉🎉🎉
		} {
			r, _ := p.mapRange(Range{at(gen, c.gen, c.delta), at(gen, c.gen, c.delta+1)}, doc)
			if want := at(src, c.src, c.delta); r == nil || r.(Range).Start != want {
				t.Errorf("%s maps to %v, want %v", c.gen, r, want)
			}
		}
	}
	check()

	if p.docOf(pathToURI(filepath.Join(dir, "later.go"))) != nil {
		t.Error("a hand-written file has a document")
	}

	src := read(egoPath)
	writeFiles(t, dir, map[string]string{"users.ego": strings.Replace(src, "GetAll", "GetEvery", -1)})
	if p.docOf(goURI) != nil {
		t.Error("stale generated Go has a document")
	}

	writeFiles(t, dir, map[string]string{"users.ego": "// More lines.\n\n" + src})
	res, err := lower.Generate(lower.Config{Dir: dir, NoLines: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range res.Outputs {
		writeFiles(t, dir, map[string]string{filepath.Base(out.Go): string(out.Code)})
	}
	check()
}

// Results that land in the generated Go of .ego files the editor doesn't
// have open, in the package asked about or in another, point at the .ego
// files; those in hand-written Go stay as gopls gives them.
func TestEditorElsewhere(t *testing.T) {
	repo, _ := filepath.Abs("../..")
	src, err := os.ReadFile(filepath.Join(repo, "internal/egotest/users/users.ego"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":          "module example.com/tmp\n\ngo 1.26.0\n",
		"go.work":         "go 1.26.0\n\nuse (\n\t.\n\t" + repo + "\n)\n",
		"users/users.ego": string(src) + "\n// Both calls Active, in more.ego.\neffect (s *Service) Both(id UserID) (bool, error) {\n\treturn s.Active(id)\n}\n",
		"users/more.ego":  "package users\n\n// Active reports whether the user loads.\neffect (s *Service) Active(id UserID) (bool, error) {\n\t_, err := s.GetAll([]UserID{id})\n\treturn err == nil, nil\n}\n",
		"users/plain.go":  "package users\n\nimport \"context\"\n\n// First is plain Go.\nfunc (s *Service) First(ctx context.Context, id UserID) (User, error) {\n\tus, err := s.GetAll(ctx, []UserID{id})\n\tif err != nil {\n\t\treturn User{}, err\n\t}\n\treturn us[0], nil\n}\n",
		"app/app.ego":     "package app\n\nimport \"example.com/tmp/users\"\n\ntype mem struct{}\n\neffect (m mem) Find(id users.UserID) (users.User, bool, error) {\n\treturn users.User{}, false, nil\n}\n\neffect All(s *users.Service, ids []users.UserID) ([]users.User, error) {\n\treturn s.GetAll(ids)\n}\n\neffect Active(s *users.Service, id users.UserID) (bool, error) {\n\treturn s.Active(id)\n}\n\neffect First(s *users.Service, id users.UserID) (users.User, error) {\n\treturn s.First(id)\n}\n",
	}
	root := t.TempDir()
	writeFiles(t, root, files)
	generate(t, filepath.Join(root, "users"))
	generate(t, filepath.Join(root, "app"))
	cl, _ := start(t, root)

	// loc is a location as file:line:character, the file relative to root.
	type location struct {
		URI   string
		Range Range
	}
	loc := func(file, needle string, delta int) string {
		p := at(files[file], needle, delta)
		return fmt.Sprintf("%s:%d:%d", file, p.Line, p.Character)
	}
	locs := func(res json.RawMessage) []string {
		var ls []location
		json.Unmarshal(res, &ls)
		var out []string
		for _, l := range ls {
			rel, _ := filepath.Rel(root, uriToPath(l.URI))
			out = append(out, fmt.Sprintf("%s:%d:%d", filepath.ToSlash(rel), l.Range.Start.Line, l.Range.Start.Character))
		}
		sort.Strings(out)
		return out
	}
	open := func(file string) string {
		uri := pathToURI(filepath.Join(root, file))
		cl.c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "ego", "version": 1, "text": files[file]}})
		cl.waitDiags(uri, func(ds []map[string]any) bool { return len(ds) == 0 })
		return uri
	}
	request := func(method, file, needle string, delta int) json.RawMessage {
		params := map[string]any{"textDocument": map[string]any{"uri": pathToURI(filepath.Join(root, file))}, "position": at(files[file], needle, delta)}
		if method == "textDocument/references" {
			params["context"] = map[string]any{"includeDeclaration": true}
		}
		return cl.call(method, params)
	}

	app := open("app/app.ego")
	for _, c := range []struct {
		method, at string
		delta      int
		want       string
	}{
		{"definition", "s.GetAll(ids)", 2, loc("users/users.ego", "GetAll(ids []UserID)", 0)},
		{"definition", "s.Active(id)", 2, loc("users/more.ego", "Active(id UserID)", 0)},
		{"definition", "users.Service", 6, loc("users/users.ego", "Service struct", 0)},
		{"definition", "s.First(id)", 2, loc("users/plain.go", "First(ctx", 0)},
		{"implementation", "Find(id users.UserID)", 0, loc("users/users.ego", "Find(id UserID)", 0)},
	} {
		got := locs(request("textDocument/"+c.method, "app/app.ego", c.at, c.delta))
		if fmt.Sprint(got) != fmt.Sprint([]string{c.want}) {
			t.Errorf("%s of %s: %v, want %s", c.method, c.at, got, c.want)
		}
	}
	cl.c.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": app}})
	cl.waitDiags(app, func(ds []map[string]any) bool { return len(ds) == 0 })

	open("users/users.ego")
	if got, want := locs(request("textDocument/definition", "users/users.ego", "s.Active(id)", 2)), loc("users/more.ego", "Active(id UserID)", 0); fmt.Sprint(got) != fmt.Sprint([]string{want}) {
		t.Errorf("definition in the package's other .ego file: %v, want %s", got, want)
	}
	got := locs(request("textDocument/references", "users/users.ego", "GetAll(ids []UserID)", 0))
	want := []string{
		loc("app/app.ego", "s.GetAll(ids)", 2),
		loc("users/more.ego", "s.GetAll(", 2),
		loc("users/plain.go", "s.GetAll(", 2),
		loc("users/users.ego", "GetAll(ids []UserID)", 0),
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("references:\n%v, want\n%v", got, want)
	}
}

// The proxy counts in UTF-16, LSP's default: neither gopls nor the editor
// may pick another encoding.
func TestPositionEncoding(t *testing.T) {
	var sent bytes.Buffer // to gopls
	p := newProxy(Config{}, newConn(strings.NewReader(""), io.Discard), newConn(strings.NewReader(""), &sent))
	params := `{"capabilities":{"general":{"positionEncodings":["utf-8","utf-16"]}}}`
	if err := p.handleEditor(&message{ID: json.RawMessage("1"), Method: "initialize", Params: json.RawMessage(params)}); err != nil {
		t.Fatal(err)
	}
	m, err := newConn(&sent, nil).read()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(m.Params), "positionEncodings") {
		t.Errorf("to gopls: %s", m.Params)
	}
	if res := adjustCapabilities(json.RawMessage(`{"capabilities":{"positionEncoding":"utf-8"}}`)); strings.Contains(string(res), "positionEncoding") {
		t.Errorf("to the editor: %s", res)
	}
}

// The shorthand maps too: hover inside a lambda and inside an f-string.
func TestEditorShorthand(t *testing.T) {
	cl, root := start(t, "")
	path := filepath.Join(root, "internal/egotest/report/report.ego")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	uri := pathToURI(path)
	cl.c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "ego", "version": 1, "text": src}})
	cl.waitDiags(uri, func(ds []map[string]any) bool { return len(ds) == 0 })
	for _, c := range []struct {
		at    string
		delta int
		want  string
	}{
		{"o => o.Late", 7, "Late bool"},                        // a field, through an inferred lambda parameter
		{"(a, b) => cmp.Compare(b.Total", 24, "Total float64"}, // a two-parameter lambda
		{"{len(orders)}", 5, "orders []Order"},                 // inside an f-string
	} {
		res := cl.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at(src, c.at, c.delta)})
		if !strings.Contains(string(res), c.want) {
			t.Errorf("hover at %q: %s", c.at, res)
		}
	}
}

// An open file that build constraints exclude here is compiled where they
// hold: it gets the compiler's diagnostics and gopls's, and saving it
// writes its Go.
func TestExcludedFile(t *testing.T) {
	other := "linux"
	if runtime.GOOS == other {
		other = "darwin"
	}
	repo, _ := filepath.Abs("../..")
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"go.mod":  "module example.com/tmp\n\ngo 1.26.0\n",
		"go.work": "go 1.26.0\n\nuse (\n\t.\n\t" + repo + "\n)\n",
		"p/a.ego": "package p\n\nfunc Shared() int { return 1 }\n",
	})
	generate(t, filepath.Join(root, "p"))
	cl, _ := start(t, root)
	name := "x_" + other + ".ego"
	path := filepath.Join(root, "p", name)
	src := "package p\n\nimport \"strconv\"\n\nfunc Bad() int {\n\tn := check strconv.Atoi(\"1\")\n\treturn n + Shared() + nope\n}\n"
	writeFiles(t, root, map[string]string{"p/" + name: src})
	uri := pathToURI(path)
	cl.c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "ego", "version": 1, "text": src}})
	for _, want := range []struct{ msg, at string }{{"check needs a function that returns an error", "check"}, {"undefined: nope", "nope"}} {
		line := at(src, want.at, 0).Line
		cl.waitDiags(uri, func(ds []map[string]any) bool {
			for _, d := range ds {
				r, _ := d["range"].(map[string]any)
				start, _ := r["start"].(map[string]any)
				if strings.Contains(fmt.Sprint(d["message"]), want.msg) && start["line"] == float64(line) {
					return true
				}
			}
			return false
		})
	}

	fixed := "package p\n\nfunc Good() int { return Shared() }\n"
	writeFiles(t, root, map[string]string{"p/" + name: fixed})
	cl.c.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": 2}, "contentChanges": []any{map[string]any{"text": fixed}}})
	cl.c.notify("textDocument/didSave", map[string]any{"textDocument": map[string]any{"uri": uri}})
	cl.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at(fixed, "Shared", 0)})
	if code, err := os.ReadFile(filepath.Join(root, "p", lower.GoName(name))); err != nil || !strings.Contains(string(code), "//go:build "+other) {
		t.Errorf("saved: %v\n%s", err, code)
	}
}
