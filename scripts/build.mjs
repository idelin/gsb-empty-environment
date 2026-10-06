// Build helper: compiles the project with a locally available TypeScript
// installation (no network access in this environment).
import { createRequire } from 'node:module';
import fs from 'node:fs';
import path from 'node:path';

const candidates = [
  process.env.TYPESCRIPT_PATH,
  '/Applications/Cursor.app/Contents/Resources/app/extensions/node_modules/typescript/lib/typescript.js',
  '/Applications/Qoder.app/Contents/Resources/app/node_modules/typescript/lib/typescript.js',
  '/Applications/Qoder.app/Contents/Resources/app/extensions/node_modules/typescript/lib/typescript.js',
].filter(Boolean);

const tsPath = candidates.find((p) => fs.existsSync(p));
if (!tsPath) {
  console.error('No local TypeScript found. Set TYPESCRIPT_PATH to a typescript.js');
  process.exit(1);
}

const require = createRequire(import.meta.url);
const ts = require(tsPath);

const root = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');
const configPath = ts.findConfigFile(root, ts.sys.fileExists, 'tsconfig.json');
const configFile = ts.readConfigFile(configPath, ts.sys.readFile);
const parsed = ts.parseJsonConfigFileContent(configFile.config, ts.sys, root);

const program = ts.createProgram(parsed.fileNames, parsed.options);
const emitResult = program.emit();
const diagnostics = ts.getPreEmitDiagnostics(program).concat(emitResult.diagnostics);

for (const d of diagnostics) {
  const msg = ts.flattenDiagnosticMessageText(d.messageText, '\n');
  if (d.file && d.start !== undefined) {
    const { line, character } = d.file.getLineAndCharacterOfPosition(d.start);
    console.error(`${d.file.fileName}:${line + 1}:${character + 1} - TS${d.code}: ${msg}`);
  } else {
    console.error(`TS${d.code}: ${msg}`);
  }
}

if (diagnostics.some((d) => d.category === ts.DiagnosticCategory.Error) || emitResult.emitSkipped) {
  console.error('Build failed.');
  process.exit(1);
}
console.log(`Built ${parsed.fileNames.length} files with TypeScript ${ts.version}.`);
