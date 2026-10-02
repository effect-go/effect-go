# effect-go for VS Code

Highlighting for `.ego` files, plus hover, go to definition, diagnostics,
rename and format-on-save through `ego lsp`, which runs gopls on the
generated Go and maps positions back.

Install the tools, then the extension:

```bash
go install github.com/effect-go/effect-go/cmd/ego@latest
go install golang.org/x/tools/gopls@latest
cd editors/vscode && npm install && npx @vscode/vsce package
code --install-extension effect-go-0.1.0.vsix
```

Settings: `ego.path` and `ego.goplsPath` if the commands aren't on your PATH.
Keep the Go extension installed: it handles `.go` files, including the
generated `_ego.go` files.
