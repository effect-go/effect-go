#!/bin/sh
# Builds effect-go-<version>.zip against an installed JetBrains IDE with the
# LSP API (GoLand, IntelliJ IDEA Ultimate…). Usage: ./build.sh [IDE.app]
set -eu
cd "$(dirname "$0")"
ide=${1:-/Applications/GoLand.app}
lib="$ide/Contents/lib"
version=$(sed -n 's/.*"version": "\(.*\)".*/\1/p' ../vscode/package.json)
out=build/effect-go
rm -rf build && mkdir -p "$out/lib" "$out/bundle" build/classes
cp=$(find "$lib" "$ide/Contents/plugins/textmate-plugin/lib" -name '*.jar' | tr '\n' ':')
"$ide/Contents/jbr/Contents/Home/bin/javac" --release 21 -nowarn -cp "$cp" -d build/classes $(find src -name '*.java')
sed "s|<idea-plugin>|<idea-plugin>\n  <version>$version</version>|" resources/META-INF/plugin.xml > build/plugin.xml
mkdir -p build/classes/META-INF && mv build/plugin.xml build/classes/META-INF/
(cd build/classes && zip -qr ../effect-go/lib/effect-go.jar .)
# The TextMate bundle is the VS Code extension's grammar.
cp -r ../vscode/package.json ../vscode/language-configuration.json ../vscode/syntaxes "$out/bundle/"
(cd build && zip -qr "effect-go-$version.zip" effect-go)
echo "built editors/jetbrains/build/effect-go-$version.zip"
