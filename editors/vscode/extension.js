// The effect-go extension starts `ego lsp` for .ego files. ego lsp runs its
// own gopls; the Go extension keeps handling .go files.
const vscode = require('vscode');
const { LanguageClient } = require('vscode-languageclient/node');

let client;

function activate(context) {
  const config = vscode.workspace.getConfiguration('ego');
  const server = {
    command: config.get('path') || 'ego',
    args: ['lsp', '-gopls', config.get('goplsPath') || 'gopls'],
  };
  client = new LanguageClient('ego', 'effect-go', server, {
    documentSelector: [{ scheme: 'file', language: 'ego' }],
  });
  client.start();
  context.subscriptions.push({ dispose: () => client && client.stop() });
}

function deactivate() {
  return client ? client.stop() : undefined;
}

module.exports = { activate, deactivate };
