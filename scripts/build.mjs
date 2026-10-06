// 把 src/focus-manager.ts 擦除类型后输出到 dist/，并动态导入验证语法。
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { stripTypes } from './strip-types.mjs';

const srcUrl = new URL('../src/focus-manager.ts', import.meta.url);
const distUrl = new URL('../dist/focus-manager.mjs', import.meta.url);

const source = await readFile(srcUrl, 'utf8');
const code = stripTypes(source);
await mkdir(new URL('../dist/', import.meta.url), { recursive: true });
await writeFile(distUrl, code);
await import(`data:text/javascript;charset=utf-8,${encodeURIComponent(code)}`);
console.log('dist/focus-manager.mjs written and verified');
