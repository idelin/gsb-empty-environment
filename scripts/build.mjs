// 极简类型剥离器：把 src/*.ts 转为 dist/*.js（环境无网络，无法安装 tsc）。
// 仅支持 src/ 中使用的受控 TS 子集：
//   - import type / export type { ... } 行
//   - interface 与 type 别名声明
//   - 参数、返回值、变量、类字段上的单行类型注解（命名类型、数组、泛型）
// 明确不使用：显式泛型实参、as 断言、非空断言、可选参数(?)、枚举、
//   正则字面量、对象字面量直接作为函数实参、标识符前的三元冒号。
import { readdirSync, readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';

const SRC = new URL('../src/', import.meta.url).pathname;
const OUT = new URL('../dist/', import.meta.url).pathname;

function isWordChar(c) {
  return /[A-Za-z0-9_$]/.test(c);
}

export function stripTypes(input) {
  const s = input
    .replace(/^[ \t]*import\s+type\s[^;]*;?[ \t]*$/gm, '')
    .replace(/^[ \t]*export\s+type\s*\{[\s\S]*?\}\s*from\s*['"][^'"]*['"];?/m, '');
  const n = s.length;
  let out = '';
  let i = 0;
  let prevToken = 'start';
  let prevPrevToken = 'start';
  let parenDepth = 0;
  const braceStack = [];
  let expectClassBrace = false;

  const setToken = (t) => {
    prevPrevToken = prevToken;
    prevToken = t;
  };

  const skipWs = (j) => {
    while (j < n && /\s/.test(s[j])) j++;
    return j;
  };

  const skipString = (j, quote) => {
    j++;
    while (j < n) {
      if (s[j] === '\\') {
        j += 2;
        continue;
      }
      if (s[j] === quote) return j + 1;
      j++;
    }
    return j;
  };

  const skipLineComment = (j) => {
    while (j < n && s[j] !== '\n') j++;
    return j;
  };

  const skipBlockComment = (j) => {
    j += 2;
    while (j < n && !(s[j] === '*' && s[j + 1] === '/')) j++;
    return j + 2;
  };

  const skipBalanced = (j) => {
    // s[j] 必须是 '{'；跳过到匹配的 '}' 之后，尊重字符串/模板/注释。
    let depth = 0;
    while (j < n) {
      const c = s[j];
      if (c === "'" || c === '"') {
        j = skipString(j, c);
        continue;
      }
      if (c === '`') {
        j = skipTemplate(j);
        continue;
      }
      if (c === '/' && s[j + 1] === '/') {
        j = skipLineComment(j);
        continue;
      }
      if (c === '/' && s[j + 1] === '*') {
        j = skipBlockComment(j);
        continue;
      }
      if (c === '{') depth++;
      else if (c === '}') {
        depth--;
        if (depth === 0) return j + 1;
      }
      j++;
    }
    return j;
  };

  const skipTemplate = (j) => {
    j++;
    while (j < n) {
      if (s[j] === '\\') {
        j += 2;
        continue;
      }
      if (s[j] === '`') return j + 1;
      if (s[j] === '$' && s[j + 1] === '{') {
        j = skipBalanced(j + 1);
        continue;
      }
      j++;
    }
    return j;
  };

  const skipTypeAlias = (j) => {
    // 从 'type' 之后扫到深度 0 的 ';'（只计 {([，避免 => 干扰）。
    let depth = 0;
    while (j < n) {
      const c = s[j];
      if (c === "'" || c === '"') {
        j = skipString(j, c);
        continue;
      }
      if (c === '`') {
        j = skipTemplate(j);
        continue;
      }
      if (c === '{' || c === '(' || c === '[') depth++;
      else if (c === '}' || c === ')' || c === ']') depth--;
      else if (c === ';' && depth === 0) return j + 1;
      j++;
    }
    return j;
  };

  const skipAnnotation = (j) => {
    // j 指向 ':'；返回到终止符（不消费）。终止符：深度 0 的 , ) = { ; 或换行。
    j++;
    let angle = 0;
    let depth = 0;
    while (j < n) {
      const c = s[j];
      if (c === '\n') return j;
      if (c === '<') {
        angle++;
        j++;
        continue;
      }
      if (c === '>') {
        if (angle > 0) angle--;
        j++;
        continue;
      }
      if (c === '(' || c === '[') {
        depth++;
        j++;
        continue;
      }
      if (c === '{') {
        if (depth === 0 && angle === 0) return j;
        depth++;
        j++;
        continue;
      }
      if (c === ')' || c === ']' || c === '}') {
        if (depth === 0 && angle === 0) return j;
        depth--;
        j++;
        continue;
      }
      if ((c === ',' || c === '=' || c === ';') && depth === 0 && angle === 0) return j;
      j++;
    }
    return j;
  };

  while (i < n) {
    const c = s[i];

    if (c === '/' && s[i + 1] === '/') {
      const j = skipLineComment(i);
      out += s.slice(i, j);
      i = j;
      continue;
    }
    if (c === '/' && s[i + 1] === '*') {
      const j = skipBlockComment(i);
      out += s.slice(i, j);
      i = j;
      continue;
    }
    if (c === "'" || c === '"') {
      const j = skipString(i, c);
      out += s.slice(i, j);
      i = j;
      setToken('string');
      continue;
    }
    if (c === '`') {
      const j = skipTemplate(i);
      out += s.slice(i, j);
      i = j;
      setToken('string');
      continue;
    }

    if (/[A-Za-z_$]/.test(c)) {
      let j = i + 1;
      while (j < n && isWordChar(s[j])) j++;
      const word = s.slice(i, j);
      if (word === 'interface') {
        let k = skipWs(j);
        while (k < n && isWordChar(s[k])) k++;
        k = skipWs(k);
        if (s[k] === '{') {
          i = skipBalanced(k);
          setToken('start');
          continue;
        }
      }
      if (word === 'type') {
        let k = skipWs(j);
        if (k < n && /[A-Za-z_$]/.test(s[k])) {
          while (k < n && isWordChar(s[k])) k++;
          k = skipWs(k);
          if (s[k] === '=') {
            i = skipTypeAlias(j);
            setToken('start');
            continue;
          }
        }
      }
      if (word === 'class') {
        expectClassBrace = true;
      }
      out += word;
      i = j;
      setToken(word);
      continue;
    }

    if (c === ':') {
      const atClassTop = braceStack[braceStack.length - 1] === 'class';
      const afterDeclKeyword =
        prevPrevToken === 'const' || prevPrevToken === 'let' || prevPrevToken === 'var';
      const isAnnotation =
        prevToken === ')' ||
        (/^[A-Za-z_$]/.test(prevToken) && (parenDepth > 0 || afterDeclKeyword || atClassTop));
      if (isAnnotation) {
        i = skipAnnotation(i);
        continue;
      }
      out += c;
      i++;
      setToken(':');
      continue;
    }

    if (c === '(') {
      parenDepth++;
      out += c;
      i++;
      setToken('(');
      continue;
    }
    if (c === ')') {
      parenDepth--;
      out += c;
      i++;
      setToken(')');
      continue;
    }
    if (c === '{') {
      braceStack.push(expectClassBrace ? 'class' : 'block');
      expectClassBrace = false;
      out += c;
      i++;
      setToken('{');
      continue;
    }
    if (c === '}') {
      braceStack.pop();
      out += c;
      i++;
      setToken('}');
      continue;
    }

    out += c;
    if (!/\s/.test(c)) {
      setToken(c);
    }
    i++;
  }
  return out;
}

mkdirSync(OUT, { recursive: true });
for (const file of readdirSync(SRC)) {
  if (!file.endsWith('.ts')) continue;
  const stripped = stripTypes(readFileSync(join(SRC, file), 'utf8'));
  writeFileSync(join(OUT, file.replace(/\.ts$/, '.js')), stripped);
  console.log('stripped', file);
}
