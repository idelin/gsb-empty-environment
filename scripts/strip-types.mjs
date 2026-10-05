// 零依赖的开发用转译器：把 src/*.ts 擦除类型后输出到 build/*.js。
// 仅支持本仓库源码使用的 TypeScript 子集（接口、类型别名、类型注解、访问修饰符）。
// 有网络的环境建议改用官方 tsc（见 tsconfig.json）。
import { readFileSync, writeFileSync, mkdirSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

const SRC_DIR = 'src';
const OUT_DIR = 'build';

function isIdentStart(ch) {
  return /[A-Za-z_]/.test(ch);
}

function isTypeChar(ch) {
  return /[A-Za-z0-9_$.\[\]|& \t]/.test(ch);
}

// 从冒号位置尝试擦除一段类型注解，成功返回 { res, next }，失败返回 null。
function tryStripAnnotation(line, colonIdx, res) {
  let j = colonIdx + 1;
  while (j < line.length && (line[j] === ' ' || line[j] === '\t')) {
    j++;
  }
  if (j >= line.length || !isIdentStart(line[j])) {
    return null;
  }
  let k = j;
  let angle = 0;
  while (k < line.length) {
    const c = line[k];
    if (c === '<') {
      angle++;
      k++;
      continue;
    }
    if (c === '>') {
      angle--;
      if (angle < 0) {
        return null;
      }
      k++;
      continue;
    }
    if (c === ',') {
      if (angle === 0) {
        return finish(line, res, k);
      }
      k++;
      continue;
    }
    if (angle === 0 && (c === ')' || c === '=' || c === ';' || c === '{')) {
      return finish(line, res, k);
    }
    if (isTypeChar(c)) {
      k++;
      continue;
    }
    return null;
  }
  return finish(line, res, k);
}

function finish(line, res, next) {
  let out = res;
  if (out.endsWith('?')) {
    const before = out[out.length - 2] || '';
    if (/[A-Za-z0-9_)]/.test(before)) {
      out = out.slice(0, -1);
    }
  }
  return { res: out, next };
}

function makeLineStripper() {
  const braceStack = [];
  let inBlockComment = false;
  let inTemplate = false;

  function significantInfo(text) {
    let i = text.length - 1;
    while (i >= 0 && (text[i] === ' ' || text[i] === '\t')) {
      i--;
    }
    const ch = i >= 0 ? text[i] : '';
    let word = '';
    let j = i;
    while (j >= 0 && /[A-Za-z0-9_$]/.test(text[j])) {
      word = text[j] + word;
      j--;
    }
    return { ch, word };
  }

  return function stripLine(line) {
    let res = '';
    let i = 0;
    let inString = null;
    while (i < line.length) {
      const c = line[i];
      if (inBlockComment) {
        const end = line.indexOf('*/', i);
        if (end < 0) {
          return res;
        }
        i = end + 2;
        inBlockComment = false;
        continue;
      }
      if (inString) {
        res += c;
        if (c === '\\') {
          res += line[i + 1] || '';
          i += 2;
          continue;
        }
        if (c === inString) {
          inString = null;
        }
        i++;
        continue;
      }
      if (inTemplate) {
        res += c;
        if (c === '\\') {
          res += line[i + 1] || '';
          i += 2;
          continue;
        }
        if (c === '`') {
          inTemplate = false;
        }
        i++;
        continue;
      }
      if (c === '/' && line[i + 1] === '/') {
        res += line.slice(i);
        break;
      }
      if (c === '/' && line[i + 1] === '*') {
        inBlockComment = true;
        i += 2;
        continue;
      }
      if (c === "'" || c === '"') {
        inString = c;
        res += c;
        i++;
        continue;
      }
      if (c === '`') {
        inTemplate = true;
        res += c;
        i++;
        continue;
      }
      if (c === '{') {
        const info = significantInfo(res);
        const isObject = info.ch === '=' || info.ch === '(' || info.ch === '[' || info.ch === ',' || info.ch === ':' || info.word === 'return';
        braceStack.push(isObject ? 'o' : 'b');
        res += c;
        i++;
        continue;
      }
      if (c === '}') {
        braceStack.pop();
        res += c;
        i++;
        continue;
      }
      if (c === ':' && braceStack[braceStack.length - 1] !== 'o') {
        const stripped = tryStripAnnotation(line, i, res);
        if (stripped) {
          res = stripped.res;
          i = stripped.next;
          continue;
        }
        res += c;
        i++;
        continue;
      }
      res += c;
      i++;
    }
    return res;
  };
}

function stripFile(source) {
  const lines = source.split('\n');
  const stripLine = makeLineStripper();
  const out = [];
  let i = 0;
  while (i < lines.length) {
    const trimmed = lines[i].trimStart();
    if (trimmed.startsWith('interface ') || trimmed.startsWith('export interface ')) {
      let depth = 0;
      do {
        for (const ch of lines[i]) {
          if (ch === '{') {
            depth++;
          }
          if (ch === '}') {
            depth--;
          }
        }
        i++;
      } while (depth > 0 && i < lines.length);
      continue;
    }
    if (trimmed.startsWith('type ') || trimmed.startsWith('export type ')) {
      i++;
      continue;
    }
    if (trimmed.startsWith('import type ')) {
      i++;
      continue;
    }
    const stripped = stripLine(lines[i]).replace(/\b(private|public|protected|readonly)\s+/g, '');
    out.push(stripped);
    i++;
  }
  return out.join('\n');
}

mkdirSync(OUT_DIR, { recursive: true });
const files = readdirSync(SRC_DIR).filter(name => name.endsWith('.ts'));
for (const name of files) {
  const source = readFileSync(join(SRC_DIR, name), 'utf8');
  const js = stripFile(source);
  const outName = name.replace(/\.ts$/, '.js');
  writeFileSync(join(OUT_DIR, outName), `// Generated by scripts/strip-types.mjs, do not edit.\n${js}`);
  console.log(`stripped ${name} -> ${join(OUT_DIR, outName)}`);
}
