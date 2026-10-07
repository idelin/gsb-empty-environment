// 离线环境没有 tsc：src/amount.ts 仅使用「可擦除」类型语法，
// 本脚本做受限的类型擦除生成 dist/amount.mjs，并用 node --check 校验语法。
// 约束（修改 amount.ts 时遵守）：
//   - interface 的结束 `}` 顶格；type 别名单行；
//   - 不用泛型 / 对象字面量类型 / as 断言 / 枚举；
//   - 类型标注只含标识符、数组后缀与 `|` 联合。
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const srcPath = fileURLToPath(new URL('../src/amount.ts', import.meta.url));
const outPath = fileURLToPath(new URL('../dist/amount.mjs', import.meta.url));

const src = readFileSync(srcPath, 'utf8');
let out = src;
out = out.replace(/^(?:export )?interface \w+ \{[\s\S]*?^\}/gm, '');
out = out.replace(/^export type [^\n]*\n/gm, '');
out = out.replace(/\)\s*:\s*[A-Za-z_$][\w$\[\] |]*(?=\s*\{)/g, ')');
out = out.replace(/\)\s*:\s*[A-Za-z_$][\w$\[\] |]*(?=\s*=>)/g, ')');
out = out.replace(/([,(]\s*)([A-Za-z_$][\w$]*)\??\s*:\s*[A-Za-z_$][\w$\[\] |]*(?=\s*[,)])/g, '$1$2');
out = out.replace(/\b(?:const|let|var)\s+[A-Za-z_$][\w$]*\s*:\s*[A-Za-z_$][\w$\[\] |]*(?=\s*=)/g, (m) => m.replace(/\s*:.*$/s, ''));

mkdirSync(new URL('../dist/', import.meta.url), { recursive: true });
writeFileSync(outPath, out);
execFileSync(process.execPath, ['--check', outPath], { stdio: 'inherit' });
console.log('built dist/amount.mjs');
