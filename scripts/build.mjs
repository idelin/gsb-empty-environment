/**
 * 离线构建：本环境无法安装 typescript/tsc（网络受限），
 * 源文件仅使用“可擦除类型”子集（interface/type 别名/类型注解/访问修饰符），
 * 因此用受限的类型擦除器产出可运行的 ESM。
 * 若环境可安装 typescript，可直接 `npx tsc`（见 tsconfig.json）。
 */
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const src = readFileSync(join(root, 'src', 'counter.ts'), 'utf8');

let out = src;

// 1. 删除 interface 块（本文件中 interface 的结束 `}` 位于行首）
out = out.replace(/^[ \t]*export\s+interface\s[\s\S]*?^\}/gm, '');

// 2. 删除 type 别名（单行）
out = out.replace(/^[ \t]*export\s+type\s[^\n]*\n/gm, '');

// 3. 擦除类型注解：标识符后可选 `?`，跟 `: Type`，
//    仅当 Type 之后紧跟 , ) = ; 或 { 或行尾（避免误伤对象字面量与三元表达式）
out = out.replace(
  /([\w)])\??\s*:\s*[A-Za-z][A-Za-z0-9]*(?:<[^;=(){}]*>)?(?:\[\])?(?=\s*[,)=;]|\s*\{|\s*$)/g,
  '$1',
);

// 4. 删除访问/只读修饰符（源码中 ReadonlyXxx 类型名含大写 R，不受影响）
out = out.replace(/\b(private|public|protected|readonly)\s+/g, '');

// 5. 压缩多余空行
out = out.replace(/\n{3,}/g, '\n\n');

mkdirSync(join(root, 'dist'), { recursive: true });
writeFileSync(join(root, 'dist', 'counter.mjs'), out);

// 自检：产出必须可被 Node 加载
const mod = await import(join(root, 'dist', 'counter.mjs'));
for (const name of [
  'emptyState', 'valueOf', 'applyOp', 'mergeStates',
  'serializeState', 'deserializeState', 'canonicalState', 'CounterReplica',
]) {
  if (typeof mod[name] === 'undefined') {
    throw new Error(`build check failed: missing export ${name}`);
  }
}
console.log('built dist/counter.mjs');
