# effect-go for VS Code

Highlighting for `.ego` files, plus hover, go to definition, diagnostics,
rename and format-on-save through `ego lsp`, which runs gopls on the
generated Go and maps positions back.

Install the tools, then the extension:

```bash
go install github.com/effect-go/effect-go/cmd/ego@latest
go install golang.org/x/tools/gopls@latest
cd editors/vscode && npm install && npx @vscode/vsce package
code --install-extension effect-go-0.3.1.vsix
```

Generated files: Go needs `x_ego.go` in the same directory as `x.ego` (a
directory is a package), so the explorer nests each one under its `.ego`
file. Set `ego.hideGenerated` to hide them from the explorer and search
altogether.

Settings: `ego.path` and `ego.goplsPath` if the commands aren't on your PATH.
Keep the Go extension installed: it handles `.go` files, including the
generated `_ego.go` files.
