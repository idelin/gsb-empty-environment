// 开发辅助运行器：本环境无网络、无法安装 typescript，
// 这里用一个极简“类型擦除器”把可擦除语法子集的 TypeScript 转为 ESM 后直接运行。
// 支持的子集：interface 声明、变量/参数/返回值类型注解、类型导入过滤。
// 刻意不使用：enum、类、泛型语法、as 断言、非空断言、正则字面量（含引号/花括号）。
// 正式环境请直接使用 tsc（见 tsconfig.json）。
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { dirname, join, resolve, relative } from 'node:path';
import { pathToFileURL } from 'node:url';

const root = process.cwd();
const outRoot = join(root, '.build');
const entryArg = process.argv[2];
if (!entryArg) {
  console.error('用法: node tools/run.mjs <入口.ts>');
  process.exit(1);
}

// ---------- 基础扫描工具 ----------
function skipString(src, i) {
  const quote = src[i];
  for (let j = i + 1; j < src.length; j++) {
    if (src[j] === '\\') { j++; continue; }
    if (src[j] === quote) return j;
    if (quote !== '`' && src[j] === '\n') return j;
  }
  return src.length - 1;
}

function skipLineComment(src, i) {
  const end = src.indexOf('\n', i + 2);
  return end === -1 ? src.length : end;
}

function skipBlockComment(src, i) {
  const end = src.indexOf('*/', i + 2);
  return end === -1 ? src.length : end + 2;
}

function matchBrace(src, open) {
  const openCh = src[open];
  const closeCh = openCh === '{' ? '}' : openCh === '(' ? ')' : ']';
  let depth = 0;
  for (let j = open; j < src.length; j++) {
    const c = src[j];
    if (c === "'" || c === '"' || c === '`') { j = skipString(src, j); continue; }
    if (c === '/' && src[j + 1] === '/') { j = skipLineComment(src, j); continue; }
    if (c === '/' && src[j + 1] === '*') { j = skipBlockComment(src, j) - 1; continue; }
    if (c === openCh) depth++;
    else if (c === closeCh) {
      depth--;
      if (depth === 0) return j;
    }
  }
  throw new Error('括号不匹配: ' + openCh + ' @' + open);
}

// ---------- 加载依赖图 ----------
const files = new Map(); // 绝对路径 -> 源码
function load(abs) {
  if (files.has(abs)) return;
  const src = readFileSync(abs, 'utf8');
  files.set(abs, src);
  for (const m of src.matchAll(/from\s+['"](\.[^'"]+)['"]/g)) {
    load(resolve(dirname(abs), m[1] + '.ts'));
  }
}
load(resolve(root, entryArg));

// ---------- 第一遍：收集各文件声明的接口名 ----------
const typeNames = new Map();
for (const [abs, src] of files) {
  const names = new Set();
  for (const m of src.matchAll(/(?:^|\n)[ \t]*(?:export[ \t]+)?interface\s+(\w+)/g)) {
    names.add(m[1]);
  }
  typeNames.set(abs, names);
}

// ---------- 变换 1：删除 interface 声明 ----------
function removeInterfaces(src) {
  const re = /(?:^|\n)[ \t]*(?:export[ \t]+)?interface\s+\w+[^{]*\{/g;
  let out = '';
  let last = 0;
  let m;
  while ((m = re.exec(src)) !== null) {
    const brace = src.indexOf('{', m.index);
    const close = matchBrace(src, brace);
    out += src.slice(last, m.index);
    last = close + 1;
  }
  return out + src.slice(last);
}

// ---------- 变换 2：擦除类型注解 ----------
const KEYWORDS = new Set([
  'if', 'for', 'while', 'switch', 'catch', 'with', 'return', 'typeof', 'else',
  'do', 'case', 'new', 'delete', 'void', 'in', 'of', 'instanceof', 'throw',
  'yield', 'await', 'function', 'super', 'this', 'import',
]);

function stripAnnotations(src) {
  const n = src.length;
  let out = '';
  let i = 0;
  const isIdStart = c => /[A-Za-z_$]/.test(c);
  const isId = c => /[A-Za-z0-9_$]/.test(c);
  const skipWs = j => { while (j < n && /\s/.test(src[j])) j++; return j; };

  // 从 ':' 之后跳过一个类型，直到深度 0 的终止符（不消费终止符）
  function skipType(j, terminators) {
    let paren = 0, brace = 0, bracket = 0, angle = 0;
    const allZero = () => paren === 0 && brace === 0 && bracket === 0 && angle === 0;
    j = skipWs(j);
    while (j < n) {
      const c = src[j];
      if (c === "'" || c === '"' || c === '`') { j = skipString(src, j) + 1; continue; }
      if (c === '/' && src[j + 1] === '/') { j = skipLineComment(src, j); continue; }
      if (c === '/' && src[j + 1] === '*') { j = skipBlockComment(src, j); continue; }
      if (c === '=' && src[j + 1] === '>') {
        if (allZero() && terminators.has('=>')) return j;
        j += 2;
        continue;
      }
      if (c === '(') paren++;
      else if (c === ')') { if (allZero() && terminators.has(')')) return j; paren--; }
      else if (c === '{') { if (allZero() && terminators.has('{')) return j; brace++; }
      else if (c === '}') { if (allZero() && terminators.has('}')) return j; brace--; }
      else if (c === '[') bracket++;
      else if (c === ']') bracket--;
      else if (c === '<') angle++;
      else if (c === '>') { if (angle > 0) angle--; }
      else if (allZero() && terminators.has(c)) return j;
      j++;
    }
    return j;
  }

  while (i < n) {
    const c = src[i];
    if (c === "'" || c === '"' || c === '`') {
      const e = skipString(src, i);
      out += src.slice(i, e + 1);
      i = e + 1;
      continue;
    }
    if (c === '/' && src[i + 1] === '/') {
      const e = skipLineComment(src, i);
      out += src.slice(i, e);
      i = e;
      continue;
    }
    if (c === '/' && src[i + 1] === '*') {
      const e = skipBlockComment(src, i);
      out += src.slice(i, e);
      i = e;
      continue;
    }
    if (isIdStart(c)) {
      let j = i + 1;
      while (j < n && isId(src[j])) j++;
      const word = src.slice(i, j);
      if (word === 'const' || word === 'let' || word === 'var') {
        out += word;
        let k = skipWs(j);
        if (isIdStart(src[k])) {
          let e = k + 1;
          while (e < n && isId(src[e])) e++;
          out += src.slice(j, e);
          k = skipWs(e);
          if (src[k] === ':') {
            i = skipType(k + 1, new Set(['=', ';', ',']));
            continue;
          }
          i = e;
          continue;
        }
        i = j;
        continue;
      }
      out += word;
      i = j;
      continue;
    }
    if (c === '(') {
      // 判断是否为函数参数列表：看 ')' 后是否跟 '{'、':' 或 '=>'，且前一个词不是控制流关键字
      let p = out.length - 1;
      while (p >= 0 && /\s/.test(out[p])) p--;
      let w = p;
      while (w >= 0 && /[A-Za-z0-9_$]/.test(out[w])) w--;
      const prevWord = out.slice(w + 1, p + 1);
      const close = matchBrace(src, i);
      const after = skipWs(close + 1);
      const isArrow = src[after] === '=' && src[after + 1] === '>';
      const isParams = !KEYWORDS.has(prevWord) && (src[after] === '{' || src[after] === ':' || isArrow);
      if (!isParams) {
        out += c;
        i++;
        continue;
      }
      out += '(';
      i++;
      let depth = 1;
      while (i < n && depth > 0) {
        const ch = src[i];
        if (ch === "'" || ch === '"' || ch === '`') {
          const e = skipString(src, i);
          out += src.slice(i, e + 1);
          i = e + 1;
          continue;
        }
        if (ch === '/' && src[i + 1] === '/') {
          const e = skipLineComment(src, i);
          out += src.slice(i, e);
          i = e;
          continue;
        }
        if (ch === '/' && src[i + 1] === '*') {
          const e = skipBlockComment(src, i);
          out += src.slice(i, e);
          i = e;
          continue;
        }
        if (ch === '(') depth++;
        else if (ch === ')') {
          depth--;
          if (depth === 0) {
            out += ')';
            i++;
            break;
          }
          out += ch;
          i++;
          continue;
        } else if (ch === ':' && depth === 1) {
          i = skipType(i + 1, new Set([',', ')', '=']));
          continue;
        }
        out += ch;
        i++;
      }
      // 返回值类型注解
      const k = skipWs(i);
      if (src[k] === ':') {
        i = skipType(k + 1, new Set(['{', '=>']));
      }
      continue;
    }
    out += c;
    i++;
  }
  return out;
}

// ---------- 变换 3：过滤类型导入、改写模块路径 ----------
function rewriteImports(src, selfAbs) {
  return src
    .replace(/import\s*\{([^}]*)\}\s*from\s*['"](\.[^'"]+)['"];?/g, (whole, names, spec) => {
      const target = resolve(dirname(selfAbs), spec + '.ts');
      const tnames = typeNames.get(target) ?? new Set();
      const kept = names
        .split(',')
        .map(s => s.trim())
        .filter(s => s.length > 0)
        .filter(s => !tnames.has(s.split(/\s+as\s+/)[0].trim()));
      if (kept.length === 0) return '';
      return `import { ${kept.join(', ')} } from '${spec}.mjs';`;
    })
    .replace(/import\s+['"](\.[^'"]+)['"];?/g, (whole, spec) => `import '${spec}.mjs';`);
}

// ---------- 输出并运行 ----------
const outputs = new Map();
for (const [abs, src] of files) {
  let js = removeInterfaces(src);
  js = stripAnnotations(js);
  js = rewriteImports(js, abs);
  const rel = relative(root, abs).replace(/\.ts$/, '.mjs');
  const outPath = join(outRoot, rel);
  mkdirSync(dirname(outPath), { recursive: true });
  writeFileSync(outPath, js);
  outputs.set(abs, outPath);
}

await import(pathToFileURL(outputs.get(resolve(root, entryArg))).href);
