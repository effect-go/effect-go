# effect-go for GoLand

Support for `.ego` files in GoLand and the other JetBrains IDEs with
language server support (IntelliJ IDEA Ultimate…), version 2024.2 or later:
highlighting, and hover, go to definition, completion, diagnostics, rename
and formatting through `ego lsp`.

It needs `ego` and `gopls`, on the PATH or in `~/go/bin`:

```bash
go install github.com/effect-go/effect-go/cmd/ego@latest
go install golang.org/x/tools/gopls@latest
```

## Building and installing

```bash
./build.sh            # or ./build.sh "/Applications/IntelliJ IDEA.app"
```

builds `build/effect-go-<version>.zip` with the IDE's own Java, against its
own jars. Install it with *Settings › Plugins › ⚙ › Install Plugin from
Disk…*. The highlighting is the VS Code extension's grammar, which the zip
carries.
