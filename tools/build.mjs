// 构建脚本：环境无法联网安装 typescript，优先用本地 node_modules，
// 否则回退到 Cursor 内置的 TypeScript 编译器 API。
import { createRequire } from 'module';
import path from 'path';
import { fileURLToPath } from 'url';

const require = createRequire(import.meta.url);
const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));

const candidates = [
  'typescript',
  '/Applications/Cursor.app/Contents/Resources/app/extensions/node_modules/typescript/lib/typescript.js',
];
let ts = null;
for (const c of candidates) {
  try { ts = require(c); break; } catch {}
}
if (!ts) {
  console.error('TypeScript compiler not found. Install typescript or adjust candidates in tools/build.mjs');
  process.exit(1);
}
console.log(`using typescript ${ts.version}`);

const options = {
  target: ts.ScriptTarget.ES2019,
  module: ts.ModuleKind.CommonJS,
  strict: true,
  noUncheckedIndexedAccess: false,
  esModuleInterop: true,
  rootDir: path.join(root, 'src'),
  outDir: path.join(root, 'dist'),
  skipLibCheck: true,
};

const program = ts.createProgram(
  ['shims.d.ts', 'ir.ts', 'interp.ts', 'opt.ts', 'randgen.ts', 'difftest.ts', 'main.ts'].map((f) => path.join(root, 'src', f)),
  options,
);
const emitResult = program.emit();
const diagnostics = ts.getPreEmitDiagnostics(program).concat(emitResult.diagnostics);
for (const d of diagnostics) {
  const msg = ts.flattenDiagnosticMessageText(d.messageText, '\n');
  if (d.file && d.start !== undefined) {
    const { line, character } = d.file.getLineAndCharacterOfPosition(d.start);
    console.error(`${d.file.fileName}:${line + 1}:${character + 1} - ${msg}`);
  } else {
    console.error(msg);
  }
}
if (diagnostics.some((d) => d.category === ts.DiagnosticCategory.Error) || emitResult.emitSkipped) {
  console.error('BUILD FAILED');
  process.exit(1);
}
console.log('build ok -> dist/');
