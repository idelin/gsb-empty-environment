// 定位 TypeScript 编译器并执行 tsc。
// 优先使用本地 devDependency（npm i -D typescript），
// 其次 TYPESCRIPT_DIR 环境变量，最后本机已知的编辑器内置副本。
import { existsSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';

const require = createRequire(import.meta.url);

const dirs = [];
try {
  dirs.push(dirname(require.resolve('typescript/package.json')));
} catch {
  // 本地未安装 typescript
}
if (process.env.TYPESCRIPT_DIR) {
  dirs.push(process.env.TYPESCRIPT_DIR);
}
dirs.push('/Applications/Qoder.app/Contents/Resources/app/node_modules/typescript');

// 标准发行版入口是 lib/tsc.js；部分内置打包（如 TS 6 dev）只有 lib/_tsc.js。
const candidates = dirs.flatMap((dir) => [
  join(dir, 'lib', 'tsc.js'),
  join(dir, 'lib', '_tsc.js'),
]);

const tsc = candidates.find((p) => existsSync(p));
if (!tsc) {
  console.error('找不到 TypeScript 编译器：请 npm i -D typescript 或设置 TYPESCRIPT_DIR');
  process.exit(1);
}

console.log(`使用 tsc: ${tsc}`);
const result = spawnSync(process.execPath, [tsc, '-p', 'tsconfig.json'], { stdio: 'inherit' });
process.exit(result.status ?? 1);
