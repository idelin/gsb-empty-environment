// Build script: type-checks and emits via the TypeScript compiler API.
// Uses the typescript package bundled with Cursor because npm registry is unreachable.
const path = require('path');
const ts = require('/Applications/Cursor.app/Contents/Resources/app/extensions/node_modules/typescript/lib/typescript.js');

const configPath = ts.findConfigFile(process.cwd(), ts.sys.fileExists, 'tsconfig.json');
const configFile = ts.readConfigFile(configPath, ts.sys.readFile);
const parsed = ts.parseJsonConfigFileContent(configFile.config, ts.sys, process.cwd());

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

if (diagnostics.length > 0 || emitResult.emitSkipped) {
  console.error('build failed');
  process.exit(1);
}
console.log('build ok');
