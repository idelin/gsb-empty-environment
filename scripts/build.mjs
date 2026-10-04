// 离线构建脚本:把 src/allocator.ts 擦除类型注解后输出 build/allocator.mjs。
// 本环境无法安装 typescript,因此用这个脚本运行测试;
// 源码只使用可擦除的类型语法(无 enum / 命名空间 / 参数属性),
// 正常环境下应优先使用 tsc(见 package.json 的 typecheck 脚本)。
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const srcPath = join(root, 'src', 'allocator.ts');
const outPath = join(root, 'build', 'allocator.mjs');

function matchParenForward(text, open) {
  let depth = 0;
  for (let i = open; i < text.length; i++) {
    if (text[i] === '(') depth++;
    else if (text[i] === ')') {
      depth--;
      if (depth === 0) return i;
    }
  }
  throw new Error('unbalanced ( at ' + open);
}

function matchParenBackward(text, close) {
  let depth = 0;
  for (let i = close; i >= 0; i--) {
    if (text[i] === ')') depth++;
    else if (text[i] === '(') {
      depth--;
      if (depth === 0) return i;
    }
  }
  throw new Error('unbalanced ) at ' + close);
}

// 移除 interface 块与单行 type 别名
function removeTypeBlocks(src) {
  const lines = src.split('\n');
  const out = [];
  let skipping = false;
  for (const line of lines) {
    if (skipping) {
      if (/^\s*\}/.test(line)) skipping = false;
      continue;
    }
    if (/^\s*(export\s+)?interface\s+[A-Za-z_$]/.test(line)) {
      skipping = true;
      continue;
    }
    if (/^\s*(export\s+)?type\s+[A-Za-z_$][\w$]*\s*=/.test(line)) continue;
    out.push(line);
  }
  return out.join('\n');
}

// 收集函数声明与箭头函数的参数列表区间
function collectParamSpans(text) {
  const spans = [];
  const fnRe = /\bfunction\s+[A-Za-z_$][\w$]*\s*\(/g;
  let m;
  while ((m = fnRe.exec(text)) !== null) {
    const open = text.indexOf('(', m.index);
    spans.push([open, matchParenForward(text, open)]);
  }
  const arRe = /=>/g;
  while ((m = arRe.exec(text)) !== null) {
    let j = m.index - 1;
    while (j >= 0 && /\s/.test(text[j])) j--;
    if (text[j] === ')') {
      spans.push([matchParenBackward(text, j), j]);
    }
  }
  return spans;
}

function stripSpan(text, open, close) {
  const inner = text
    .slice(open + 1, close)
    .replace(/([A-Za-z_$][\w$]*)\s*:\s*[A-Za-z][\w<>, \[\]|]*(?=\s*[,)=]|$)/g, '$1');
  let after = text.slice(close + 1);
  const ret = after.match(/^\s*:\s*[A-Za-z][\w<>, \[\]|]*(?=\s*[{=])/);
  if (ret) after = after.slice(ret[0].length);
  return text.slice(0, open + 1) + inner + text.slice(close, close + 1) + after;
}

let text = removeTypeBlocks(readFileSync(srcPath, 'utf8'));
const spans = collectParamSpans(text).sort((a, b) => b[0] - a[0]);
for (const [open, close] of spans) {
  text = stripSpan(text, open, close);
}
text = text.replace(/\bnew\s+([A-Za-z_$][\w$]*)\s*<[\w<>, \[\]|]*>/g, 'new $1');
text = text.replace(
  /^(\s*(?:export\s+)?(?:const|let)\s+[A-Za-z_$][\w$]*)\s*:\s*[A-Za-z][\w<>, \[\]|]*(\s*[;=])/gm,
  '$1$2'
);

mkdirSync(dirname(outPath), { recursive: true });
writeFileSync(outPath, text);
console.log('built ' + outPath);
