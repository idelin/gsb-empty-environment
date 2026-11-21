import { createRequire } from 'node:module';
import path from 'node:path';
import process from 'node:process';

const require = createRequire(import.meta.url);
const ts = require('/Applications/Cursor.app/Contents/Resources/app/extensions/node_modules/typescript/lib/typescript.js');

const root = process.cwd();
const configPath = ts.findConfigFile(root, ts.sys.fileExists, 'tsconfig.json');
if (!configPath) {
  console.error('tsconfig.json not found');
  process.exit(1);
}

const configFile = ts.readConfigFile(configPath, ts.sys.readFile);
if (configFile.error) {
  report([configFile.error]);
  process.exit(1);
}

const parsed = ts.parseJsonConfigFileContent(
  configFile.config,
  ts.sys,
  path.dirname(configPath),
  { noEmit: true },
);

const program = ts.createProgram({
  rootNames: parsed.fileNames,
  options: parsed.options,
  projectReferences: parsed.projectReferences,
});

const diagnostics = ts
  .getPreEmitDiagnostics(program)
  .filter((diagnostic) => diagnostic.file !== undefined);

for (const diagnostic of diagnostics) {
  const position = diagnostic.file.getLineAndCharacterOfPosition(diagnostic.start ?? 0);
  const message = ts.flattenDiagnosticMessageText(diagnostic.messageText, '\n');
  console.error(
    `${diagnostic.file.fileName}:${position.line + 1}:${position.character + 1} ` +
      `TS${diagnostic.code}: ${message}`,
  );
}

if (diagnostics.length > 0) {
  console.error(`\n${diagnostics.length} type error(s)`);
  process.exit(1);
}
console.log(`typecheck passed (TypeScript ${ts.version})`);
