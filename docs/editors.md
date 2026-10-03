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
| Neovim | config, below | Go's tree-sitter grammar, or Vim's Go syntax | yes |
| Vim | config, below | Vim's Go syntax | highlighting only |
| Helix | config, below | Go's tree-sitter grammar | no |
| Zed | needs an extension (not written yet) | | |

The configurations below follow each editor's documentation; tell us if one
needs fixing.

## VS Code

```bash
cd editors/vscode && npm install && npx @vscode/vsce package
code --install-extension effect-go-*.vsix
```

With `explorer.fileNesting.enabled` on, the explorer nests each `x_ego.go`
under its `x.ego`; the `ego.hideGenerated` setting hides them instead.

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
vim.api.nvim_create_autocmd("FileType", {
  pattern = "ego",
  callback = function(ev)
    -- Go's tree-sitter grammar if it's installed (:TSInstall go), else Vim's Go syntax.
    if not pcall(vim.treesitter.start, ev.buf, "go") then
      vim.schedule(function()
        vim.api.nvim_buf_call(ev.buf, function()
          vim.bo.syntax = "go"
          vim.cmd("syntax keyword goStatement check must fail effect match enum")
        end)
      end)
    end
  end,
})
vim.lsp.config("ego", {
  cmd = { "ego", "lsp" },
  filetypes = { "ego" },
  root_markers = { "go.mod" },
})
vim.lsp.enable("ego")
```

Format on save with `vim.lsp.buf.format()` in a `BufWritePre` autocommand,
as for any language server.

## Vim

In `.vimrc`, for highlighting:

```vim
autocmd BufNewFile,BufRead *.ego setlocal filetype=ego syntax=go
autocmd Syntax go if &filetype ==# 'ego' | syntax keyword goStatement check must fail effect match enum | endif
```

Vim has no built-in language server client: with a plugin such as
[yegappan/lsp](https://github.com/yegappan/lsp), register `ego lsp` for the
`ego` file type.

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
