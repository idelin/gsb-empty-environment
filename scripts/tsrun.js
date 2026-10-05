#!/usr/bin/env node
'use strict';

/*
 * 离线 TypeScript 运行器（sandbox 无网络，无法安装 typescript 包）。
 *
 * 对本仓库使用的「可擦除类型」子集做转译：
 *   - 移除 interface / type 别名声明
 *   - 移除函数参数、返回值、变量声明的类型标注
 *   - 移除 `as T` 断言与 `foo<T>(...)` 泛型实参
 *   - import / export 转 CommonJS
 * 转译仅做「空白化」（保留换行），因此运行时报错的行号与源码一致。
 *
 * 用法: node scripts/tsrun.js <entry.ts> [args...]
 */

const fs = require('fs');
const path = require('path');
const Module = require('module');

const MULTI_PUNCT = [
  '...', '===', '!==', '=>', '?.', '??', '==', '!=', '<=', '>=',
  '&&', '||', '++', '--', '+=', '-=', '*=', '/=', '%=', '<<', '**',
];

function tokenize(src) {
  const tokens = [];
  const n = src.length;
  let i = 0;
  while (i < n) {
    const c = src[i];
    if (c === ' ' || c === '\t' || c === '\r' || c === '\n' || c === '\f' || c === '\v') { i++; continue; }
    if (c === '/' && src[i + 1] === '/') { while (i < n && src[i] !== '\n') i++; continue; }
    if (c === '/' && src[i + 1] === '*') {
      i += 2;
      while (i < n && !(src[i] === '*' && src[i + 1] === '/')) i++;
      i = Math.min(i + 2, n);
      continue;
    }
    if (c === '"' || c === "'") {
      const s = i++;
      while (i < n && src[i] !== c) { if (src[i] === '\\') i++; i++; }
      i++;
      tokens.push({ t: 'str', s, e: i });
      continue;
    }
    if (c === '`') {
      // 模板字符串：栈式状态机。tpl=模板文本（只认 ` 和 ${），expr=${} 表达式
      // （需处理嵌套字符串/模板/注释/花括号），引号=字符串字面量。
      const s = i;
      i++;
      const modeStack = ['tpl'];
      let braceDepth = 0;
      while (i < n && modeStack.length > 0) {
        const mode = modeStack[modeStack.length - 1];
        const ch = src[i];
        if (mode === 'tpl') {
          if (ch === '\\') { i += 2; continue; }
          if (ch === '`') { i++; modeStack.pop(); continue; }
          if (ch === '$' && src[i + 1] === '{') { modeStack.push('expr'); braceDepth = 0; i += 2; continue; }
          i++;
          continue;
        }
        if (mode === "'" || mode === '"') {
          if (ch === '\\') { i += 2; continue; }
          if (ch === mode) { i++; modeStack.pop(); continue; }
          i++;
          continue;
        }
        // expr 模式
        if (ch === "'" || ch === '"') { modeStack.push(ch); i++; continue; }
        if (ch === '`') { modeStack.push('tpl'); i++; continue; }
        if (ch === '/' && src[i + 1] === '/') { while (i < n && src[i] !== '\n') i++; continue; }
        if (ch === '/' && src[i + 1] === '*') {
          i += 2;
          while (i < n && !(src[i] === '*' && src[i + 1] === '/')) i++;
          i = Math.min(i + 2, n);
          continue;
        }
        if (ch === '{') { braceDepth++; i++; continue; }
        if (ch === '}') {
          if (braceDepth === 0) { modeStack.pop(); i++; continue; }
          braceDepth--;
          i++;
          continue;
        }
        i++;
      }
      tokens.push({ t: 'str', s, e: i });
      continue;
    }
    if (/[0-9]/.test(c)) {
      const s = i;
      while (i < n && /[0-9a-zA-Z_.]/.test(src[i])) i++;
      tokens.push({ t: 'num', s, e: i });
      continue;
    }
    if (/[A-Za-z_$]/.test(c)) {
      const s = i;
      while (i < n && /[A-Za-z0-9_$]/.test(src[i])) i++;
      tokens.push({ t: 'id', s, e: i, v: src.slice(s, i) });
      continue;
    }
    let matched = null;
    for (const mp of MULTI_PUNCT) { if (src.startsWith(mp, i)) { matched = mp; break; } }
    if (matched) { tokens.push({ t: 'p', s: i, e: i + matched.length, v: matched }); i += matched.length; continue; }
    tokens.push({ t: 'p', s: i, e: i + 1, v: c });
    i++;
  }
  return tokens;
}

const OPEN_TO_CLOSE = { '(': ')', '[': ']', '{': '}' };

function transform(src, filename) {
  const tokens = tokenize(src);
  const N = tokens.length;
  const edits = [];
  const exported = [];

  const tv = (k) => (k >= 0 && k < N ? tokens[k].v : undefined);
  const tt = (k) => (k >= 0 && k < N ? tokens[k].t : undefined);
  const blank = (s, e) => edits.push({ s, e, text: null });
  const textOf = (k) => src.slice(tokens[k].s, tokens[k].e);

  function matchBracket(k) {
    const open = tokens[k].v;
    const close = OPEN_TO_CLOSE[open];
    let d = 0;
    for (let j = k; j < N; j++) {
      const v = tokens[j].v;
      if (v === open) d++;
      else if (v === close) { d--; if (d === 0) return j; }
    }
    throw new Error('unbalanced ' + open + ' in ' + filename);
  }

  // 从类型起始 token k 扫描，遇到 depth 0 的 stopVals 之一（或未匹配的右括号）停止
  function scanType(k, stopVals) {
    let depth = 0;
    let j = k;
    while (j < N) {
      const v = tokens[j].v;
      // j === k 时 '{' 是对象字面量类型的开头，而非函数体
      if (depth === 0 && j !== k && stopVals.indexOf(v) >= 0) {
        // '()' 之后的 '=>' 是函数类型的一部分（如 () => number），而非箭头函数体
        if (v === '=>' && tokens[j - 1].v === ')') { j++; continue; }
        break;
      }
      if (v === '(' || v === '[' || v === '{' || v === '<') depth++;
      else if (v === ')' || v === ']' || v === '}' || v === '>') {
        if (depth === 0) break;
        depth--;
      }
      j++;
    }
    return j;
  }

  // 每个括号帧记录未配对的三元 '?' 数，用于区分三元 ':' 与类型标注 ':'
  const rootFrame = { open: '', atStart: false, q: 0 };
  const stack = [];
  const curFrame = () => (stack.length === 0 ? rootFrame : stack[stack.length - 1]);
  const topParen = () => {
    if (stack.length === 0) return null;
    const top = stack[stack.length - 1];
    return top.open === '(' ? top : null;
  };

  let i = 0;
  while (i < N) {
    const tk = tokens[i];
    const v = tk.v;
    const frameAtStart = topParen();

    // ---- 括号帧维护 ----
    if (v === '(' || v === '[' || v === '{') {
      stack.push({ open: v, atStart: true, q: 0 });
      i++;
      continue;
    }
    if (v === ')' || v === ']' || v === '}') {
      stack.pop();
      if (v === ')' && tv(i + 1) === ':' && curFrame().q === 0) {
        // 函数返回类型
        const stop = scanType(i + 2, ['{', '=>', ';', ',']);
        blank(tokens[i + 1].s, tokens[stop].s);
        i = stop;
        continue;
      }
      if ((v === '}' || v === ']') && topParen() && topParen().atStart &&
          tv(i + 1) === ':' && curFrame().q === 0) {
        // 解构参数的标注: function f({a, b}: Opts)
        const stop = scanType(i + 2, [',', ')', '=']);
        blank(tokens[i + 1].s, tokens[stop].s);
        topParen().atStart = false;
        i = stop;
        continue;
      }
      i++;
      continue;
    }
    if (v === ',') {
      const tp = topParen();
      if (tp) tp.atStart = true;
      i++;
      continue;
    }
    if (v === '?') {
      curFrame().q++;
      i++;
      continue;
    }
    if (v === ':') {
      if (curFrame().q > 0) curFrame().q--;
      i++;
      continue;
    }

    // ---- import ----
    if (tk.t === 'id' && v === 'import' && tv(i - 1) !== '.') {
      let semi = i;
      let depth = 0;
      while (semi < N && !(tokens[semi].v === ';' && depth === 0)) {
        const sv = tokens[semi].v;
        if (sv === '(' || sv === '[' || sv === '{') depth++;
        else if (sv === ')' || sv === ']' || sv === '}') depth--;
        semi++;
      }
      if (tv(i + 1) === 'type') {
        blank(tk.s, tokens[semi].e);
      } else if (tt(i + 1) === 'str') {
        edits.push({ s: tk.s, e: tokens[semi].e, text: 'require(' + textOf(i + 1) + ');' });
      } else if (tv(i + 1) === '{') {
        const close = matchBracket(i + 1);
        const specs = [];
        let k = i + 2;
        while (k < close) {
          if (tt(k) === 'id' && tv(k) !== 'as') {
            if (tv(k + 1) === 'as') { specs.push(textOf(k) + ': ' + textOf(k + 2)); k += 3; }
            else { specs.push(textOf(k)); k += 1; }
          } else k++;
        }
        edits.push({ s: tk.s, e: tokens[semi].e, text: 'const { ' + specs.join(', ') + ' } = require(' + textOf(close + 2) + ');' });
      } else if (tv(i + 1) === '*') {
        edits.push({ s: tk.s, e: tokens[semi].e, text: 'const ' + textOf(i + 3) + ' = require(' + textOf(i + 5) + ');' });
      } else if (tt(i + 1) === 'id') {
        edits.push({ s: tk.s, e: tokens[semi].e, text: 'const ' + textOf(i + 1) + ' = require(' + textOf(i + 3) + ').default;' });
      } else {
        throw new Error('unsupported import in ' + filename);
      }
      i = semi + 1;
      continue;
    }

    // ---- export ----
    if (tk.t === 'id' && v === 'export' && tv(i - 1) !== '.') {
      const nv = tv(i + 1);
      if (nv === 'default') {
        edits.push({ s: tk.s, e: tokens[i + 1].e, text: 'module.exports =' });
        i += 2;
        continue;
      }
      if (nv === '{') {
        let semi = i + 1;
        while (semi < N && tokens[semi].v !== ';') semi++;
        let k = i + 2;
        while (k < N && tokens[k].v !== '}') {
          if (tt(k) === 'id' && tv(k) !== 'as') {
            if (tv(k + 1) === 'as') { exported.push({ local: textOf(k), name: textOf(k + 2) }); k += 3; }
            else { exported.push({ local: textOf(k), name: textOf(k) }); k += 1; }
          } else k++;
        }
        blank(tk.s, tokens[semi].e);
        i = semi + 1;
        continue;
      }
      if (nv === 'type' || nv === 'interface') {
        blank(tk.s, tk.e);
        i++;
        continue;
      }
      if (nv === 'function' || nv === 'const' || nv === 'let' || nv === 'var' || nv === 'class') {
        exported.push({ local: textOf(i + 2), name: textOf(i + 2) });
        blank(tk.s, tk.e);
        i++;
        continue;
      }
      if (nv === 'async' && tv(i + 2) === 'function') {
        exported.push({ local: textOf(i + 3), name: textOf(i + 3) });
        blank(tk.s, tk.e);
        i++;
        continue;
      }
      throw new Error('unsupported export form in ' + filename);
    }

    // ---- interface ----
    if (tk.t === 'id' && v === 'interface' && tv(i - 1) !== '.' && tt(i + 1) === 'id') {
      let j = i + 2;
      while (j < N && tokens[j].v !== '{') j++;
      const close = matchBracket(j);
      blank(tk.s, tokens[close].e);
      i = close + 1;
      continue;
    }

    // ---- type 别名 ----
    if (tk.t === 'id' && v === 'type' && tv(i - 1) !== '.' && tt(i + 1) === 'id' &&
        (tv(i + 2) === '=' || tv(i + 2) === '<')) {
      let j = i + 2;
      let depth = 0;
      while (j < N) {
        const vv = tokens[j].v;
        if (vv === '(' || vv === '[' || vv === '{' || vv === '<') depth++;
        else if (vv === ')' || vv === ']' || vv === '}' || vv === '>') depth--;
        else if (vv === ';' && depth === 0) break;
        j++;
      }
      blank(tk.s, tokens[j].e);
      i = j + 1;
      continue;
    }

    // ---- const/let/var 标注 ----
    if (tk.t === 'id' && (v === 'const' || v === 'let' || v === 'var')) {
      if (tt(i + 1) === 'id' && tv(i + 2) === ':') {
        const stop = scanType(i + 3, [',', '=', ';', ')', 'of']);
        blank(tokens[i + 2].s, tokens[stop].s);
        i = stop;
        continue;
      }
      if ((tv(i + 1) === '{' || tv(i + 1) === '[')) {
        const close = matchBracket(i + 1);
        if (tv(close + 1) === ':') {
          const stop = scanType(close + 2, [',', '=', ';', ')', 'of']);
          blank(tokens[close + 1].s, tokens[stop].s);
          i = stop;
          continue;
        }
      }
      i++;
      continue;
    }

    // ---- as 断言 ----
    if (tk.t === 'id' && v === 'as' && tv(i - 1) !== '.') {
      let j = i + 1;
      for (;;) {
        if (j >= N) break;
        const t2 = tokens[j];
        if (t2.t === 'id') { j++; continue; }
        if (t2.v === '.') { j++; continue; }
        if (t2.v === '[' && tv(j + 1) === ']') { j += 2; continue; }
        if (t2.v === '<') {
          let d = 0;
          do { if (tokens[j].v === '<') d++; else if (tokens[j].v === '>') d--; j++; } while (d > 0 && j < N);
          continue;
        }
        if (t2.v === '|' || t2.v === '&') { j++; continue; }
        break;
      }
      blank(tk.s, tokens[j - 1].e);
      i = j;
      continue;
    }

    // ---- 参数标注: 在参数列表顶层、位于 '(' 或 ',' 之后的 ident ----
    if (tk.t === 'id' && frameAtStart && frameAtStart.atStart) {
      let colonIdx = -1;
      if (tv(i + 1) === ':') colonIdx = i + 1;
      else if (tv(i + 1) === '?' && tv(i + 2) === ':') colonIdx = i + 2;
      if (colonIdx >= 0) {
        const stop = scanType(colonIdx + 1, [',', ')', '=']);
        blank(tokens[colonIdx].s, tokens[stop].s);
        frameAtStart.atStart = false;
        i = stop;
        continue;
      }
    }

    // ---- 泛型实参: ident '<' ... '>' '(' ----
    if (tk.t === 'id' && tv(i + 1) === '<') {
      let j = i + 2;
      let d = 1;
      let ok = true;
      while (j < N && d > 0) {
        const t2 = tokens[j];
        if (t2.t === 'id' || t2.t === 'str' || t2.t === 'num') { j++; continue; }
        if (t2.v === '<') { d++; j++; continue; }
        if (t2.v === '>') { d--; j++; continue; }
        if (t2.t === 'p' && [',', '.', '[', ']', '|', '&', '?'].indexOf(t2.v) >= 0) { j++; continue; }
        ok = false;
        break;
      }
      if (ok && d === 0 && tv(j) === '(') {
        blank(tokens[i + 1].s, tokens[j - 1].e);
        i = j;
        continue;
      }
      i++;
      continue;
    }

    if (frameAtStart && v !== '...') frameAtStart.atStart = false;
    i++;
  }

  // 应用编辑（不允许重叠）
  edits.sort((a, b) => a.s - b.s);
  let out = '';
  let pos = 0;
  for (const e of edits) {
    if (e.s < pos) throw new Error('overlapping edits in ' + filename);
    out += src.slice(pos, e.s);
    out += e.text === null ? src.slice(e.s, e.e).replace(/[^\n]/g, ' ') : e.text;
    pos = e.e;
  }
  out += src.slice(pos);
  if (exported.length > 0) {
    out += '\n' + exported.map((x) => 'exports.' + x.name + ' = ' + x.local + ';').join('\n') + '\n';
  }
  return out;
}

function compileFile(module_, filename) {
  const src = fs.readFileSync(filename, 'utf8');
  module_._compile(transform(src, filename), filename);
}

module.exports = { tokenize, transform };

if (require.main === module) {
  Module._extensions['.ts'] = compileFile;
  const args = process.argv.slice(2);
  if (args.length === 0) {
    console.error('usage: node scripts/tsrun.js <entry.ts> [args...]');
    process.exit(64);
  }
  const target = path.resolve(args[0]);
  process.argv = [process.argv[0], target].concat(args.slice(1));
  require(target);
}
