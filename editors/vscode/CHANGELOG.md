# Changelog

## 0.4.2

An icon for the extension, and one for `.ego` files in the explorer, tabs
and the language picker.

## 0.4.1

The extension no longer turns on the explorer's file nesting for every
workspace, which also nested files like lock files in projects without
`.ego` files. It still adds the patterns that nest `x_ego.go` under `x.ego`:
turn on `explorer.fileNesting.enabled` to use them.

## 0.4.0

The first release on the Marketplace and Open VSX: highlighting for `.ego`
files, and hover, go to definition, diagnostics, rename and format-on-save
through `ego lsp`. Generated `_ego.go` files are nested under their `.ego`
file, or hidden with `ego.hideGenerated`.
