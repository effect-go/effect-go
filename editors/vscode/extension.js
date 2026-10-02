// The effect-go extension starts `ego lsp` for .ego files. ego lsp runs its
// own gopls; the Go extension keeps handling .go files.
const vscode = require('vscode');
const { LanguageClient } = require('vscode-languageclient/node');

let client;

// The files ego generate writes next to the sources.
const GENERATED = ['**/*_ego.go', '**/*_ego_test.go', '**/layers_ego.go'];

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

  syncHidden();
  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration('ego.hideGenerated')) syncHidden();
    }),
  );
}

// syncHidden adds the generated files to the workspace's files.exclude when
// ego.hideGenerated is on, and removes them when it's off. It only touches
// those patterns.
async function syncHidden() {
  if (!vscode.workspace.workspaceFolders) return;
  const hide = vscode.workspace.getConfiguration('ego').get('hideGenerated');
  const files = vscode.workspace.getConfiguration('files');
  const current = files.inspect('exclude').workspaceValue || {};
  const next = { ...current };
  for (const g of GENERATED) {
    if (hide) next[g] = true;
    else delete next[g];
  }
  if (JSON.stringify(next) === JSON.stringify(current)) return;
  await files.update('exclude', Object.keys(next).length ? next : undefined, vscode.ConfigurationTarget.Workspace);
}

function deactivate() {
  return client ? client.stop() : undefined;
}

module.exports = { activate, deactivate };
