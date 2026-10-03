/**
 * 离线构建脚本：通过 TypeScript 编译器 API 编译 tsconfig.json 中的工程。
 *
 * TypeScript 解析顺序：
 *   1. 本地 node_modules（npm install typescript 后）
 *   2. 环境变量 TYPESCRIPT_PATH 指向的 typescript.js
 *   3. 本机 Cursor 内置的 TypeScript（兜底）
 */
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const require = createRequire(import.meta.url);
const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));

const candidates = [
  'typescript',
  process.env.TYPESCRIPT_PATH,
  '/Applications/Cursor.app/Contents/Resources/app/extensions/node_modules/typescript/lib/typescript.js',
].filter(Boolean);

let ts = null;
for (const candidate of candidates) {
  try {
    ts = require(candidate);
    break;
  } catch {
    // try next
  }
}
if (!ts) {
  console.error('未找到 TypeScript 编译器。请安装 typescript，或设置 TYPESCRIPT_PATH 指向 typescript.js');
  process.exit(1);
}

const configPath = ts.findConfigFile(root, ts.sys.fileExists, 'tsconfig.json');
const configFile = ts.readConfigFile(configPath, ts.sys.readFile);
const parsed = ts.parseJsonConfigFileContent(configFile.config, ts.sys, root);

const program = ts.createProgram(parsed.fileNames, parsed.options);
const emitResult = program.emit();
const diagnostics = ts.getPreEmitDiagnostics(program).concat(emitResult.diagnostics);

if (diagnostics.length > 0) {
  const host = {
    getCanonicalFileName: (f) => f,
    getCurrentDirectory: () => root,
    getNewLine: () => '\n',
  };
  console.error(ts.formatDiagnosticsWithColorAndContext(diagnostics, host));
}
const errors = diagnostics.filter((d) => d.category === ts.DiagnosticCategory.Error);
if (errors.length > 0 || emitResult.emitSkipped) {
  console.error(`构建失败：${errors.length} 个错误`);
  process.exit(1);
}
console.log(`构建成功（TypeScript ${ts.version}）-> dist/`);
