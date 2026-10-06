// 无依赖的 TypeScript 类型擦除器。
//
// 本环境没有 tsc / 网络，无法安装 typescript。本模块把 src/ 下受控子集的
// TS 源码转换为语义等价的 JS，供测试直接运行；类型纯擦除，不改变运行行为。
//
// 支持的子集（src 代码须遵守）：
// - interface / type 别名（成员类型中不写内联对象字面量类型）
// - 参数、返回值、变量、类字段的类型注解（泛型最多一层嵌套）
// - import type、as 大写类型断言、类/函数的泛型声明、implements
// 限制：
// - 不用 enum / namespace / 参数属性 / 非空断言 `!` / 装饰器
// - 不用 switch-case 与标签语句（冒号形态与字段注解冲突）
// - 模板字符串插值内不写类型语法；源码中不用正则字面量
// - 三元表达式不要写成 `cond ? (a) : x => y` 这种形态

import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const TYPE = String.raw`[A-Za-z_$][\w$]*(?:<[^<>;{}]*>)?(?:\s*\[\s*\])?(?:\s*\|\s*[A-Za-z_$][\w$]*(?:<[^<>;{}]*>)?(?:\s*\[\s*\])?)*`;

const IMPORT_TYPE_RE = /^[ \t]*import\s+type\s+[\s\S]*?from\s*['"][^'"]*['"][ \t]*;?[ \t]*\n/gm;
const INTERFACE_RE = /^[ \t]*(?:export\s+)?interface\s+[A-Za-z_$][\w$]*(?:\s+extends\s+[A-Za-z_$][\w$]*(?:\s*,\s*[A-Za-z_$][\w$]*)*)?[ \t]*\{[^}]*\}[ \t]*;?[ \t]*\n?/gm;
const TYPE_ALIAS_RE = /^[ \t]*(?:export\s+)?type\s+[A-Za-z_$][\w$]*(?:<[^<>]*>)?\s*=[^;{]*;[ \t]*\n?/gm;

// 把字符串 / 模板 / 注释替换为占位符，后续转换只在纯代码上进行。
function extractTrivia(source) {
  const trivia = [];
  let out = '';
  let i = 0;
  const n = source.length;
  while (i < n) {
    const ch = source[i];
    const next = source[i + 1];
    if (ch === "'" || ch === '"' || ch === '`') {
      let j = i + 1;
      while (j < n) {
        if (source[j] === '\\') { j += 2; continue; }
        if (source[j] === ch) { j++; break; }
        j++;
      }
      trivia.push(source.slice(i, j));
      out += `\x00${trivia.length - 1}\x00`;
      i = j;
      continue;
    }
    if (ch === '/' && next === '/') {
      let j = i;
      while (j < n && source[j] !== '\n') j++;
      trivia.push(source.slice(i, j));
      out += `\x00${trivia.length - 1}\x00`;
      i = j;
      continue;
    }
    if (ch === '/' && next === '*') {
      let j = source.indexOf('*/', i + 2);
      j = j === -1 ? n : j + 2;
      trivia.push(source.slice(i, j));
      out += `\x00${trivia.length - 1}\x00`;
      i = j;
      continue;
    }
    out += ch;
    i++;
  }
  return { code: out, trivia };
}

function restoreTrivia(code, trivia) {
  return code.replace(/\x00(\d+)\x00/g, (whole, index) => trivia[Number(index)]);
}

// 跳过一处类型文本，返回结束下标；起始不像类型则返回 -1。
function skipType(code, i) {
  const n = code.length;
  while (i < n && /\s/.test(code[i])) i++;
  if (i >= n) return -1;
  const first = code[i];
  if (!(/[A-Za-z_$]/.test(first) || first === '(')) return -1;
  let angle = 0;
  let paren = 0;
  let bracket = 0;
  while (i < n) {
    const ch = code[i];
    if (ch === '<') { angle++; i++; continue; }
    if (ch === '>') { if (angle > 0) { angle--; i++; continue; } return i; }
    if (ch === '(') { paren++; i++; continue; }
    if (ch === ')') { if (paren === 0 && angle === 0) return i; paren--; i++; continue; }
    if (ch === '[') {
      if (code[i + 1] === ']') { i += 2; continue; }
      bracket++; i++; continue;
    }
    if (ch === ']') { if (bracket === 0) return i; bracket--; i++; continue; }
    if (ch === ',' && angle === 0 && paren === 0 && bracket === 0) return i;
    if (ch === '=' && angle === 0 && paren === 0) {
      if (code[i + 1] === '>') { i += 2; continue; }
      return i;
    }
    if (ch === '{' || ch === '}' || ch === ';') return i;
    i++;
  }
  return i;
}

// 上一个有效字符是 `(` 或 `,` 时才可能是参数注解，
// 这样对象字面量与三元表达式不会被误伤。
function atParamPosition(out) {
  let k = out.length - 1;
  while (k >= 0 && /\s/.test(out[k])) k--;
  return out[k] === '(' || out[k] === ',';
}

// 括号栈扫描：擦除参数注解与返回值注解。
function scanStrip(code) {
  const stack = [];
  let out = '';
  let i = 0;
  const n = code.length;
  while (i < n) {
    const ch = code[i];
    if (ch === '(' || ch === '[' || ch === '{') { stack.push(ch); out += ch; i++; continue; }
    if (ch === ')') {
      stack.pop();
      out += ch;
      i++;
      let k = i;
      while (k < n && /\s/.test(code[k])) k++;
      if (code[k] === ':') {
        const afterType = skipType(code, k + 1);
        if (afterType !== -1) {
          let m = afterType;
          while (m < n && /\s/.test(code[m])) m++;
          if (code[m] === '{' || (code[m] === '=' && code[m + 1] === '>')) {
            out += ' ';
            i = m;
            continue;
          }
        }
      }
      continue;
    }
    if (ch === '}' || ch === ']') { stack.pop(); out += ch; i++; continue; }
    if (/[A-Za-z_$]/.test(ch)) {
      if (stack[stack.length - 1] === '(' && atParamPosition(out)) {
        const m = /^[A-Za-z_$][\w$]*\??\s*:/.exec(code.slice(i));
        if (m !== null) {
          const name = /^[A-Za-z_$][\w$]*/.exec(m[0])[0];
          const afterType = skipType(code, i + m[0].length);
          if (afterType !== -1) {
            out += name;
            i = afterType;
            continue;
          }
        }
      }
      let j = i + 1;
      while (j < n && /[\w$]/.test(code[j])) j++;
      out += code.slice(i, j);
      i = j;
      continue;
    }
    out += ch;
    i++;
  }
  return out;
}

function transformCode(code) {
  let out = code;
  // 变量声明注解：const x: T = ...
  out = out.replace(new RegExp(`\\b(const|let|var)(\\s+[A-Za-z_$][\\w$]*)\\s*:\\s*${TYPE}`, 'g'), '$1$2');
  // 类字段（行首，注解后紧跟 = 或 ;；对象字面量属性后跟逗号或 }，不会误伤）
  out = out.replace(
    new RegExp(`^([ \\t]*)(?:(?:public|private|protected|readonly|static|abstract|declare|override)\\s+)*([A-Za-z_$][\\w$]*)\\??\\s*:\\s*${TYPE}(?=\\s*[;=])`, 'gm'),
    '$1$2',
  );
  // 无注解的访问修饰符：private foo = 1 / private method()
  out = out.replace(/(?<![.\w$])(?:public|private|protected|readonly|abstract|declare|override)\s+(?=[A-Za-z_$])/g, '');
  // as 断言（仅大写开头类型或 as const）
  out = out.replace(/\s+as\s+(?:const|[A-Z][\w$]*(?:<[^<>;{}]*>)?(?:\[\])?)/g, '');
  // 泛型声明与 new 表达式的类型实参
  out = out.replace(/\bnew\s+([A-Za-z_$][\w$]*)\s*<[^<>;{}]*>/g, 'new $1');
  out = out.replace(/\b(class\s+[A-Za-z_$][\w$]*)\s*<[^<>;{}]*>/g, '$1');
  out = out.replace(/\b(function\s+[A-Za-z_$][\w$]*)\s*<[^<>;{}]*>/g, '$1');
  // implements 子句
  out = out.replace(/\s*\bimplements\s+[A-Za-z_$][\w$]*(?:\s*,\s*[A-Za-z_$][\w$]*)*(?=\s*\{)/g, ' ');
  return out;
}

export function stripTypes(source) {
  let src = String(source);
  src = src.replace(IMPORT_TYPE_RE, '');
  src = src.replace(INTERFACE_RE, '');
  src = src.replace(TYPE_ALIAS_RE, '');
  const { code, trivia } = extractTrivia(src);
  const transformed = scanStrip(transformCode(code));
  return restoreTrivia(transformed, trivia);
}

export async function importTs(url) {
  const source = await readFile(fileURLToPath(url), 'utf8');
  const code = stripTypes(source);
  return import(`data:text/javascript;charset=utf-8,${encodeURIComponent(code)}`);
}
