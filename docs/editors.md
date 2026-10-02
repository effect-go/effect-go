# Editors

`ego lsp` is a language server for `.ego` files: it runs gopls on the
generated Go and maps positions back, for hover, go to definition,
completion, diagnostics, rename and formatting. It needs `ego` and `gopls`
on the PATH. Any editor that can start a language server for a file type can
use it; `.go` files stay with gopls as usual.

| Editor | Setup | Highlighting | Tested |
|---|---|---|---|
| VS Code | the extension in [editors/vscode](../editors/vscode) | effect-go grammar | yes |
| GoLand, IntelliJ | LSP4IJ + TextMate bundle, below | effect-go grammar | no |
| Neovim | config, below | Go's tree-sitter grammar | no |
| Helix | config, below | Go's tree-sitter grammar | no |
| Zed | needs an extension (not written yet) | | |

The configurations below follow each editor's documentation; tell us if one
needs fixing.

## VS Code

```bash
cd editors/vscode && npm install && npx @vscode/vsce package
code --install-extension effect-go-*.vsix
```

It nests each `x_ego.go` under its `x.ego` in the explorer; the
`ego.hideGenerated` setting hides them instead.

## GoLand and IntelliJ

1. Install the [LSP4IJ](https://plugins.jetbrains.com/plugin/23257-lsp4ij)
   plugin. In *Languages & Frameworks › Language Servers*, add a server with
   the command `ego lsp`, and in *Mappings › File name patterns* map `*.ego`
   to the language id `ego`.
2. For highlighting, in *Editor › TextMate Bundles*, add the
   `editors/vscode` directory: IntelliJ reads the grammar from the VS Code
   extension.

## Neovim (0.11 and later)

```lua
vim.filetype.add({ extension = { ego = "ego" } })
vim.treesitter.language.register("go", "ego") -- Go's grammar highlights most of it
vim.lsp.config("ego", {
  cmd = { "ego", "lsp" },
  filetypes = { "ego" },
  root_markers = { "go.mod" },
})
vim.lsp.enable("ego")
```

## Helix

In `languages.toml`:

```toml
[language-server.ego]
command = "ego"
args = ["lsp"]

[[language]]
name = "ego"
scope = "source.ego"
file-types = ["ego"]
roots = ["go.mod"]
grammar = "go"
language-servers = ["ego"]
comment-token = "//"
indent = { tab-width = 4, unit = "\t" }
auto-format = true
```

## Zed

Zed adds languages only through extensions, which need a tree-sitter
grammar. Until an effect-go extension exists, `.ego` files open as plain
text there.
