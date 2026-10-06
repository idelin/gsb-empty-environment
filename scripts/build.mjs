// 受控子集的 TypeScript 类型擦除构建（环境无 tsc/网络时的本地构建）。
// src/ 中的源码遵守约定：类型仅出现在 interface/type 块、单行函数签名、
// 以及单行变量声明注解中；不使用泛型、枚举、装饰器、as 断言。
import { readdirSync, readFileSync, writeFileSync, mkdirSync, rmSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const SRC = join(root, 'src');
const DIST = join(root, 'dist');

function stripOneParam(segment) {
  let depth = 0;
  for (let i = 0; i < segment.length; i++) {
    const ch = segment[i];
    if ('([{<'.includes(ch)) depth++;
    else if (')]}>'.includes(ch)) depth--;
    else if (ch === ':' && depth === 0) {
      return segment.slice(0, i).trim().replace(/\?$/, '');
    } else if (ch === '=' && depth === 0) {
      throw new Error('default parameter values are not supported: ' + segment);
    }
  }
  return segment.trim();
}

function stripParams(params) {
  const segments = [];
  let depth = 0;
  let current = '';
  for (const ch of params) {
    if ('([{<'.includes(ch)) depth++;
    else if (')]}>'.includes(ch)) depth--;
    if (ch === ',' && depth === 0) {
      segments.push(current);
      current = '';
    } else {
      current += ch;
    }
  }
  if (current.trim()) segments.push(current);
  return segments.map(stripOneParam).join(', ');
}

function stripReturnType(rest) {
  const match = rest.match(/^\s*:\s*[A-Za-z][\w<>\[\]|', ]*?(?=\s*\{|\s*=>|$)/);
  if (!match) return rest;
  return rest.slice(match[0].length);
}

function stripFunctionLine(line) {
  const fnIndex = line.indexOf('function ');
  if (fnIndex === -1) return line;
  const openIndex = line.indexOf('(', fnIndex);
  if (openIndex === -1) return line;
  let depth = 0;
  let closeIndex = -1;
  for (let i = openIndex; i < line.length; i++) {
    const ch = line[i];
    if (ch === '(') depth++;
    else if (ch === ')') {
      depth--;
      if (depth === 0) {
        closeIndex = i;
        break;
      }
    }
  }
  if (closeIndex === -1) {
    throw new Error('function signature must fit on one line: ' + line.trim());
  }
  const params = line.slice(openIndex + 1, closeIndex);
  const rest = stripReturnType(line.slice(closeIndex + 1));
  return line.slice(0, openIndex + 1) + stripParams(params) + ')' + rest;
}

function stripVarAnnotation(line) {
  const match = line.match(/^(\s*(?:export\s+)?(?:const|let|var)\s+[A-Za-z_$][\w$]*)\s*:\s*([^=]+?)(\s*=.*)$/);
  if (match) return match[1] + match[3];
  return line;
}

function stripTypes(source, file) {
  const lines = source.split('\n');
  const out = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    const trimmed = line.trim();
    if (/^import\s+type\s/.test(trimmed)) {
      i++;
      continue;
    }
    if (/^(export\s+)?(interface|type)\s/.test(trimmed)) {
      let depth = 0;
      let started = false;
      let done = false;
      while (i < lines.length && !done) {
        for (const ch of lines[i]) {
          if (ch === '{') {
            depth++;
            started = true;
          } else if (ch === '}') {
            depth--;
          } else if (ch === ';' && !started && depth === 0) {
            done = true;
            break;
          }
        }
        if (started && depth === 0) done = true;
        i++;
      }
      if (!done) throw new Error('unterminated interface/type in ' + file);
      continue;
    }
    out.push(stripVarAnnotation(stripFunctionLine(line)));
    i++;
  }
  return out.join('\n');
}

rmSync(DIST, { recursive: true, force: true });
mkdirSync(DIST, { recursive: true });
for (const entry of readdirSync(SRC)) {
  if (!entry.endsWith('.ts')) continue;
  const source = readFileSync(join(SRC, entry), 'utf8');
  const stripped = stripTypes(source, entry);
  writeFileSync(join(DIST, entry.replace(/\.ts$/, '.js')), stripped);
  console.log('built dist/' + entry.replace(/\.ts$/, '.js'));
}
