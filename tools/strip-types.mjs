#!/usr/bin/env node
/**
 * 离线 TypeScript 类型擦除器（零依赖，供无 tsc 的环境运行测试）。
 *
 * 支持的 TS 子集（源码需遵守）：
 * - interface / type 别名 / declare 语句（整段移除，可含 export 前缀）
 * - function 声明与函数表达式的参数、返回值类型标注
 * - const / let / var 变量的类型标注（含多声明、解构，但解构内不写类型）
 * - import type / export type 语句
 *
 * 约束（源码不得使用）：
 * - 泛型、enum、namespace、class、装饰器、as 断言、非空断言 !
 * - 箭头函数的参数类型标注（用匿名 function 或让 TS 推断）
 * - 参数默认值中的三元表达式、正则字面量
 * - 行尾必须写分号（不依赖 ASI）
 */
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..');

const FILES = [
  ['src/pixel-contours.ts', 'dist/src/pixel-contours.js'],
  ['test/pixel-contours.test.ts', 'dist/test/pixel-contours.test.js'],
];

function isWsChar(ch) {
  return ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r' || ch === '\f' || ch === '\v';
}

function tokenize(src) {
  const tokens = [];
  let i = 0;
  const n = src.length;
  while (i < n) {
    const ch = src[i];
    if (isWsChar(ch)) {
      let j = i + 1;
      while (j < n && isWsChar(src[j])) j++;
      tokens.push({ t: 'ws', v: src.slice(i, j) });
      i = j;
      continue;
    }
    if (ch === '/' && src[i + 1] === '/') {
      let j = i;
      while (j < n && src[j] !== '\n') j++;
      tokens.push({ t: 'comment', v: src.slice(i, j) });
      i = j;
      continue;
    }
    if (ch === '/' && src[i + 1] === '*') {
      let j = src.indexOf('*/', i + 2);
      if (j === -1) throw new Error('未闭合的块注释');
      tokens.push({ t: 'comment', v: src.slice(i, j + 2) });
      i = j + 2;
      continue;
    }
    if (ch === "'" || ch === '"') {
      let j = i + 1;
      while (j < n && src[j] !== ch) {
        if (src[j] === '\\') j++;
        j++;
      }
      if (j >= n) throw new Error('未闭合的字符串');
      tokens.push({ t: 'str', v: src.slice(i, j + 1) });
      i = j + 1;
      continue;
    }
    if (ch === '`') {
      let j = i + 1;
      let depth = 0;
      while (j < n) {
        const cj = src[j];
        if (cj === '\\') { j += 2; continue; }
        if (cj === '`' && depth === 0) { j++; break; }
        if (cj === '$' && src[j + 1] === '{') { depth++; j += 2; continue; }
        if (cj === '}' && depth > 0) { depth--; j++; continue; }
        j++;
      }
      if (j > n) throw new Error('未闭合的模板字符串');
      tokens.push({ t: 'tpl', v: src.slice(i, j) });
      i = j;
      continue;
    }
    if (/[A-Za-z_$]/.test(ch)) {
      let j = i + 1;
      while (j < n && /[A-Za-z0-9_$]/.test(src[j])) j++;
      tokens.push({ t: 'id', v: src.slice(i, j) });
      i = j;
      continue;
    }
    if (/[0-9]/.test(ch)) {
      let j = i + 1;
      while (j < n && /[A-Za-z0-9_.]/.test(src[j])) j++;
      tokens.push({ t: 'num', v: src.slice(i, j) });
      i = j;
      continue;
    }
    tokens.push({ t: 'punct', v: ch });
    i++;
  }
  return tokens;
}

function stripTypes(tokens) {
  const out = [];
  let i = 0;

  function isWs(tk) { return tk.t === 'ws' || tk.t === 'comment'; }
  function nextSig(k) {
    let j = k + 1;
    while (j < tokens.length && isWs(tokens[j])) j++;
    return j;
  }
  function prevSig(k) {
    let j = k - 1;
    while (j >= 0 && isWs(tokens[j])) j--;
    return j;
  }
  function emit(k) { out.push(tokens[k].v); }

  /* 跳过类型记号，直到深度 0 处出现 stopChars 之一（不消耗终止符） */
  function skipType(k, stopChars) {
    let pd = 0, bd = 0, cd = 0;
    while (k < tokens.length) {
      const tk = tokens[k];
      if (tk.t === 'punct') {
        if (pd === 0 && bd === 0 && cd === 0 && stopChars.indexOf(tk.v) !== -1) break;
        if (tk.v === '(') pd++;
        else if (tk.v === ')') pd--;
        else if (tk.v === '[') bd++;
        else if (tk.v === ']') bd--;
        else if (tk.v === '{') cd++;
        else if (tk.v === '}') cd--;
      }
      k++;
    }
    return k;
  }

  /* 跳过整条语句（到深度 0 的分号为止，消耗分号） */
  function skipStatement(k) {
    let pd = 0, bd = 0, cd = 0;
    while (k < tokens.length) {
      const tk = tokens[k];
      if (tk.t === 'punct') {
        if (tk.v === ';' && pd === 0 && bd === 0 && cd === 0) return k + 1;
        if (tk.v === '(') pd++;
        else if (tk.v === ')') pd--;
        else if (tk.v === '[') bd++;
        else if (tk.v === ']') bd--;
        else if (tk.v === '{') cd++;
        else if (tk.v === '}') cd--;
      }
      k++;
    }
    throw new Error('语句缺少分号');
  }

  /* 跳过 interface 声明（从 interface 关键字处开始，消耗到匹配的 } 之后） */
  function skipInterface(k) {
    let depth = 0;
    let started = false;
    while (k < tokens.length) {
      const tk = tokens[k];
      if (tk.t === 'punct') {
        if (tk.v === '{') { depth++; started = true; }
        else if (tk.v === '}') {
          depth--;
          if (started && depth === 0) return k + 1;
        }
      }
      k++;
    }
    throw new Error('interface 缺少闭合花括号');
  }

  /* const/let/var 声明状态栈（声明可嵌套在函数体等结构内） */
  const declStack = [];
  function topDecl() {
    return declStack.length > 0 ? declStack[declStack.length - 1] : null;
  }

  while (i < tokens.length) {
    const tk = tokens[i];

    const decl = topDecl();
    if (decl !== null) {
      if (decl.mode === 'ident') {
        if (isWs(tk)) { emit(i); i++; continue; }
        if (tk.t === 'id') { emit(i); i++; decl.mode = 'after'; continue; }
        if (tk.t === 'punct' && (tk.v === '{' || tk.v === '[')) {
          const open = tk.v;
          const close = open === '{' ? '}' : ']';
          let depth = 0;
          do {
            const t2 = tokens[i];
            if (t2.t === 'punct') {
              if (t2.v === open) depth++;
              else if (t2.v === close) depth--;
            }
            emit(i);
            i++;
          } while (depth > 0 && i < tokens.length);
          decl.mode = 'after';
          continue;
        }
        declStack.pop();
        continue;
      }
      if (decl.mode === 'after') {
        if (isWs(tk)) { emit(i); i++; continue; }
        if (tk.t === 'punct' && tk.v === ':') {
          i = skipType(i + 1, ['=', ',', ';']);
          continue;
        }
        if (tk.t === 'punct' && tk.v === '=') {
          emit(i); i++;
          decl.mode = 'value';
          decl.pd = 0; decl.bd = 0; decl.cd = 0;
          continue;
        }
        if (tk.t === 'punct' && tk.v === ',') { emit(i); i++; decl.mode = 'ident'; continue; }
        if (tk.t === 'punct' && tk.v === ';') { emit(i); i++; declStack.pop(); continue; }
        /* of / in 等：声明结束，当前 token 走正常分发 */
        declStack.pop();
        continue;
      }
      /* value 模式 */
      if (tk.t === 'punct') {
        if (decl.pd === 0 && decl.bd === 0 && decl.cd === 0) {
          if (tk.v === ',') { emit(i); i++; decl.mode = 'ident'; continue; }
          if (tk.v === ';') { emit(i); i++; declStack.pop(); continue; }
        }
        if (tk.v === '(') decl.pd++;
        else if (tk.v === ')') decl.pd--;
        else if (tk.v === '[') decl.bd++;
        else if (tk.v === ']') decl.bd--;
        else if (tk.v === '{') decl.cd++;
        else if (tk.v === '}') decl.cd--;
        emit(i); i++; continue;
      }
      /* 非 punct：落入正常分发（嵌套 function / const 等也能被处理） */
    }

    if (tk.t !== 'id') { emit(i); i++; continue; }
    const word = tk.v;
    const p = prevSig(i);
    const pv = p >= 0 ? tokens[p].v : '';

    if (word === 'import' || word === 'export') {
      const q = nextSig(i);
      const qw = q < tokens.length && tokens[q].t === 'id' ? tokens[q].v : '';
      if (qw === 'type') { i = skipStatement(i); continue; }
      if (word === 'export' && qw === 'interface') { i = skipInterface(q); continue; }
      emit(i); i++; continue;
    }
    if (word === 'interface' && pv !== '.') { i = skipInterface(i); continue; }
    if (word === 'type' && pv !== '.') {
      const q = nextSig(i);
      const r = nextSig(q);
      if (
        q < tokens.length && tokens[q].t === 'id' &&
        r < tokens.length && tokens[r].t === 'punct' && tokens[r].v === '='
      ) {
        i = skipStatement(i);
        continue;
      }
      emit(i); i++; continue;
    }
    if (word === 'declare') { i = skipStatement(i); continue; }
    if (word === 'function') {
      emit(i); i++;
      while (i < tokens.length && !(tokens[i].t === 'punct' && tokens[i].v === '(')) {
        emit(i); i++;
      }
      if (i >= tokens.length) continue;
      emit(i); i++; /* ( */
      let depth = 1, brace = 0, bracket = 0;
      while (i < tokens.length && depth > 0) {
        const t2 = tokens[i];
        if (t2.t === 'punct') {
          if (t2.v === '(') { depth++; emit(i); i++; continue; }
          if (t2.v === ')') { depth--; emit(i); i++; continue; }
          if (t2.v === '{') { brace++; emit(i); i++; continue; }
          if (t2.v === '}') { brace--; emit(i); i++; continue; }
          if (t2.v === '[') { bracket++; emit(i); i++; continue; }
          if (t2.v === ']') { bracket--; emit(i); i++; continue; }
          if (t2.v === ':' && depth === 1 && brace === 0 && bracket === 0) {
            i = skipType(i + 1, [',', ')', '=']);
            continue;
          }
        }
        emit(i); i++;
      }
      const q = nextSig(i - 1);
      if (q < tokens.length && tokens[q].t === 'punct' && tokens[q].v === ':') {
        i = skipType(q + 1, ['{']);
      }
      continue;
    }
    if (word === 'const' || word === 'let' || word === 'var') {
      emit(i); i++;
      declStack.push({ mode: 'ident', pd: 0, bd: 0, cd: 0 });
      continue;
    }
    emit(i); i++;
  }
  return out.join('');
}

for (const [src, dst] of FILES) {
  const srcPath = join(ROOT, src);
  const dstPath = join(ROOT, dst);
  const code = stripTypes(tokenize(readFileSync(srcPath, 'utf8')));
  mkdirSync(dirname(dstPath), { recursive: true });
  writeFileSync(dstPath, code);
  console.log('strip: ' + src + ' -> ' + dst);
}
