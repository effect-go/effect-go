# effect-go for VS Code

Language support for [effect-go](https://github.com/effect-go/effect-go)
`.ego` files: highlighting of the dialect, and hover, go to definition,
diagnostics, rename and format-on-save through `ego lsp`, which runs gopls on
the generated Go and maps positions back.

## Requirements

The `ego` command and gopls:

```bash
go install github.com/effect-go/effect-go/cmd/ego@latest
go install golang.org/x/tools/gopls@latest
```

Keep the Go extension installed too: it handles `.go` files, including the
generated `_ego.go` files.

## Generated files

Go needs `x_ego.go` in the same directory as `x.ego` (a directory is a
package). With `explorer.fileNesting.enabled` on, the explorer nests each one
under its `.ego` file. Set `ego.hideGenerated` to hide them from the explorer
and search altogether.

## Settings

- `ego.path` and `ego.goplsPath`, if the commands aren't on your PATH.
- `ego.hideGenerated`, as above.

## Building from source

```bash
npm install && npx @vscode/vsce package
code --install-extension effect-go-0.4.0.vsix
```
