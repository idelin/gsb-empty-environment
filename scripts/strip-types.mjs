// 极简类型擦除器：把 src/*.ts 转成可运行的 dist/*.js。
// 仅支持本仓库使用的“可擦除类型”子集：
//   - interface / type 别名声明
//   - import type / export type 语句
//   - 函数声明与箭头函数的形参、返回值类型注解，变量声明类型注解
// 源码不使用泛型实参、enum、namespace、类、as 断言、可选参数、内联对象类型，
// 因此无需完整 TS 解析器。正式环境可直接使用 tsc（见 tsconfig.json）。
import { mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const SRC = join(root, 'src');
const OUT = join(root, 'dist');

const isIdentStart = (c) => /[A-Za-z_$]/.test(c);
const isIdentChar = (c) => /[A-Za-z0-9_$]/.test(c);

export function stripTypes(src) {
  const n = src.length;
  let out = '';
  let i = 0;
  let prevToken = '';
  let expectVarName = false;
  let expectVarColon = false;
  let expectFuncName = false;
  let expectFuncParen = false;
  let lastParenWasParam = false;
  const parenStack = [];

  const skipWsAndComments = () => {
    while (i < n) {
      if (src[i] === '/' && src[i + 1] === '/') {
        while (i < n && src[i] !== '\n') i++;
      } else if (src[i] === '/' && src[i + 1] === '*') {
        i += 2;
        while (i < n && !(src[i] === '*' && src[i + 1] === '/')) i++;
        i += 2;
      } else if (/\s/.test(src[i])) {
        i++;
      } else {
        break;
      }
    }
  };

  // 跳过类型注解，直到深度 0 的 , ) = ; { 之一（不消费终止符）。
  const skipTypeAnnotation = () => {
    let depth = 0;
    while (i < n) {
      skipWsAndComments();
      const c = src[i];
      if (depth === 0 && (c === ',' || c === ')' || c === '=' || c === ';' || c === '{')) return;
      if (c === '(' || c === '[' || c === '{' || c === '<') depth++;
      else if (c === ')' || c === ']' || c === '}' || c === '>') depth--;
      i++;
    }
  };

  const skipToTerminator = (terminator) => {
    let depth = 0;
    while (i < n) {
      const c = src[i];
      if (c === terminator && depth === 0) {
        i++;
        return;
      }
      if (c === '(' || c === '[' || c === '{') depth++;
      else if (c === ')' || c === ']' || c === '}') depth--;
      i++;
    }
  };

  const skipInterface = () => {
    while (i < n && src[i] !== '{') i++;
    let depth = 0;
    while (i < n) {
      if (src[i] === '{') depth++;
      else if (src[i] === '}') {
        depth--;
        if (depth === 0) {
          i++;
          return;
        }
      }
      i++;
    }
  };

  // 从 openIdx 处的 '(' 找匹配的 ')'，返回其下标（跳过字符串与注释）。
  const findMatchingParen = (openIdx) => {
    let depth = 0;
    let j = openIdx;
    while (j < n) {
      const ch = src[j];
      if (ch === '"' || ch === "'") {
        j++;
        while (j < n && src[j] !== ch) {
          if (src[j] === '\\') j++;
          j++;
        }
      } else if (ch === '/' && src[j + 1] === '/') {
        while (j < n && src[j] !== '\n') j++;
        continue;
      } else if (ch === '/' && src[j + 1] === '*') {
        j += 2;
        while (j < n && !(src[j] === '*' && src[j + 1] === '/')) j++;
        j += 2;
        continue;
      } else if (ch === '(') {
        depth++;
      } else if (ch === ')') {
        depth--;
        if (depth === 0) return j;
      }
      j++;
    }
    return -1;
  };

  // 冒号之后是否在深度 0 处出现 '=>'（用于识别箭头函数返回值注解）。
  const looksLikeArrowReturn = (colonIdx) => {
    let depth = 0;
    let j = colonIdx + 1;
    while (j < n) {
      const ch = src[j];
      if (ch === '(' || ch === '[' || ch === '<') depth++;
      else if (ch === ')' || ch === ']' || ch === '>') depth--;
      else if (ch === '{') {
        if (depth === 0) return false;
        depth++;
      } else if (ch === '}') {
        if (depth === 0) return false;
        depth--;
      } else if ((ch === ';' || ch === ',') && depth === 0) {
        return false;
      } else if (ch === '=' && src[j + 1] === '>' && depth === 0) {
        return true;
      }
      j++;
    }
    return false;
  };

  // 判断 openIdx 处的 '(' 是否为（声明或箭头）形参表。
  const isParamParen = (openIdx) => {
    const close = findMatchingParen(openIdx);
    if (close === -1) return false;
    let j = close + 1;
    while (j < n && /\s/.test(src[j])) j++;
    if (src.startsWith('=>', j)) return true;
    if (src[j] === ':') return looksLikeArrowReturn(j);
    return false;
  };

  const atStatementStart = () =>
    prevToken === '' || prevToken === ';' || prevToken === '}' || prevToken === '{' || prevToken === 'export';

  while (i < n) {
    const c = src[i];
    if (c === '/' && src[i + 1] === '/') {
      const start = i;
      while (i < n && src[i] !== '\n') i++;
      out += src.slice(start, i);
      continue;
    }
    if (c === '/' && src[i + 1] === '*') {
      const start = i;
      i += 2;
      while (i < n && !(src[i] === '*' && src[i + 1] === '/')) i++;
      i += 2;
      out += src.slice(start, i);
      continue;
    }
    if (c === '"' || c === "'") {
      const start = i;
      i++;
      while (i < n && src[i] !== c) {
        if (src[i] === '\\') i++;
        i++;
      }
      i++;
      out += src.slice(start, i);
      prevToken = 'str';
      continue;
    }
    if (c === '`') {
      const start = i;
      i++;
      let depth = 0;
      while (i < n) {
        if (src[i] === '\\') i++;
        else if (src[i] === '`' && depth === 0) break;
        else if (src[i] === '$' && src[i + 1] === '{') {
          depth++;
          i++;
        } else if (src[i] === '}' && depth > 0) depth--;
        i++;
      }
      i++;
      out += src.slice(start, i);
      prevToken = 'str';
      continue;
    }
    if (isIdentStart(c)) {
      const start = i;
      while (i < n && isIdentChar(src[i])) i++;
      const word = src.slice(start, i);
      if (word === 'interface' && atStatementStart()) {
        skipInterface();
        prevToken = ';';
        continue;
      }
      if (word === 'type' && atStatementStart()) {
        skipToTerminator(';');
        prevToken = ';';
        continue;
      }
      if (word === 'import' || word === 'export') {
        const save = i;
        skipWsAndComments();
        if (src.slice(i, i + 4) === 'type' && !isIdentChar(src[i + 4] ?? ' ')) {
          skipToTerminator(';');
          prevToken = ';';
          continue;
        }
        i = save;
      }
      if (word === 'const' || word === 'let' || word === 'var') {
        expectVarName = true;
      } else if (expectVarName) {
        expectVarName = false;
        expectVarColon = true;
      } else {
        expectVarColon = false;
      }
      if (word === 'function') {
        expectFuncName = true;
      } else if (expectFuncName) {
        expectFuncName = false;
        expectFuncParen = true;
      }
      out += word;
      prevToken = word;
      continue;
    }
    if (c === ':') {
      const inParams = parenStack.length > 0 && parenStack[parenStack.length - 1] === true;
      const isReturnType = prevToken === ')' && lastParenWasParam;
      if (inParams || expectVarColon || isReturnType) {
        i++;
        skipTypeAnnotation();
        expectVarColon = false;
        prevToken = ':type';
        continue;
      }
      out += c;
      i++;
      prevToken = ':';
      continue;
    }
    if (c === '(') {
      if (expectFuncName) {
        expectFuncName = false;
        expectFuncParen = true;
      }
      const isParam = expectFuncParen || isParamParen(i);
      parenStack.push(isParam);
      lastParenWasParam = false;
      expectFuncParen = false;
      out += c;
      i++;
      prevToken = '(';
      continue;
    }
    if (c === ')') {
      lastParenWasParam = parenStack.pop() === true;
      out += c;
      i++;
      prevToken = ')';
      continue;
    }
    if (c === '=' || c === ',' || c === ';') {
      expectVarColon = false;
      out += c;
      i++;
      prevToken = c;
      continue;
    }
    if (!/\s/.test(c)) {
      expectVarColon = false;
      expectFuncParen = false;
      prevToken = c;
    }
    out += c;
    i++;
  }
  return out;
}

function main() {
  mkdirSync(OUT, { recursive: true });
  for (const file of readdirSync(SRC)) {
    if (!file.endsWith('.ts')) continue;
    const source = readFileSync(join(SRC, file), 'utf8');
    const js = stripTypes(source);
    const target = file.replace(/\.ts$/, '.js');
    writeFileSync(join(OUT, target), js);
    console.log(`stripped src/${file} -> dist/${target}`);
  }
}

main();
