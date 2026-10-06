#!/usr/bin/env node
// scripts/strip.js — dependency-free stripper for the restricted TypeScript
// subset used in src/. The sources only use:
//   - interfaces (closing brace at column 0) and single-line type aliases
//   - type annotations in top-level function signatures
//   - `const/let name: T = ...` declarations
//   - `import {..} from '..'`, `import type ..`, `export { .. };`
// so this mechanical transform yields runnable CommonJS. Every output file
// is syntax-checked with vm.Script before being written.
'use strict';
const fs = require('fs');
const path = require('path');
const vm = require('vm');

function stripTypeBlocks(src) {
  const lines = src.split('\n');
  const out = [];
  let inBlock = false;
  for (const line of lines) {
    if (inBlock) {
      if (/^\}/.test(line)) {
        inBlock = false;
      }
      continue;
    }
    if (/^export\s+interface\s/.test(line) || /^interface\s/.test(line)) {
      inBlock = true;
      continue;
    }
    if (/^export\s+type\s/.test(line) || /^type\s/.test(line)) {
      continue;
    }
    if (/^import\s+type\s/.test(line)) {
      continue;
    }
    out.push(line);
  }
  return out.join('\n');
}

function rewriteModules(src) {
  src = src.replace(/^import\s*\{([^}]*)\}\s*from\s*('[^']*');\s*$/gm, function (m, names, p) {
    return 'const {' + names + ' } = require(' + p + ');';
  });
  src = src.replace(/^export\s*\{([^}]*)\};?\s*$/gm, function (m, names) {
    return 'module.exports = {' + names + ' };';
  });
  return src;
}

function isIdent(ch) {
  return /[A-Za-z0-9_$]/.test(ch);
}

// Skip a type annotation starting at index j (just past ':'). Returns the
// index of the first character after the annotation.
function skipType(src, j, mode) {
  let depth = 0;
  while (j < src.length) {
    const t = src[j];
    if (depth === 0) {
      if (t === '=' && src[j + 1] === '>') {
        if (mode === 'return') {
          break;
        }
        j += 2;
        continue;
      }
      if (mode === 'var') {
        if (t === ';' || t === ',' || t === '\n' || t === '=') {
          break;
        }
      } else if (mode === 'return') {
        if (t === '{') {
          break;
        }
      } else {
        if (t === ',' || t === ')' || t === '=') {
          break;
        }
      }
    }
    if (t === '(' || t === '[' || t === '<' || t === '{') {
      depth++;
    } else if (t === ')' || t === ']' || t === '>' || t === '}') {
      if (depth === 0) {
        break;
      }
      depth--;
    }
    j++;
  }
  return j;
}

function stripAnnotations(src) {
  let out = '';
  let i = 0;
  let parens = 0;
  let braces = 0;
  let state = 'code';
  let prevSig = '';
  let prevToken = '';
  let prevPrevToken = '';
  while (i < src.length) {
    const c = src[i];
    if (state === 'line') {
      out += c;
      if (c === '\n') {
        state = 'code';
      }
      i++;
      continue;
    }
    if (state === 'block') {
      if (c === '*' && src[i + 1] === '/') {
        out += '*/';
        i += 2;
        state = 'code';
      } else {
        out += c;
        i++;
      }
      continue;
    }
    if (state === 'sq' || state === 'dq' || state === 'tpl') {
      out += c;
      if (c === '\\' && i + 1 < src.length) {
        out += src[i + 1];
        i += 2;
        continue;
      }
      if ((state === 'sq' && c === "'") || (state === 'dq' && c === '"') || (state === 'tpl' && c === '`')) {
        state = 'code';
      }
      i++;
      continue;
    }
    if (c === '/' && src[i + 1] === '/') {
      state = 'line';
      out += c;
      i++;
      continue;
    }
    if (c === '/' && src[i + 1] === '*') {
      state = 'block';
      out += c;
      i++;
      continue;
    }
    if (c === "'") {
      state = 'sq';
      out += c;
      i++;
      continue;
    }
    if (c === '"') {
      state = 'dq';
      out += c;
      i++;
      continue;
    }
    if (c === '`') {
      state = 'tpl';
      out += c;
      i++;
      continue;
    }
    if (isIdent(c)) {
      let j = i;
      while (j < src.length && isIdent(src[j])) {
        j++;
      }
      const word = src.slice(i, j);
      out += word;
      prevPrevToken = prevToken;
      prevToken = word;
      prevSig = 'x';
      i = j;
      continue;
    }
    if (c === ':') {
      let mode = null;
      if (braces === 0 && parens > 0) {
        mode = 'param';
      } else if (braces === 0 && prevSig === ')') {
        mode = 'return';
      } else if (prevPrevToken === 'const' || prevPrevToken === 'let' || prevPrevToken === 'var') {
        mode = 'var';
      }
      if (mode !== null) {
        i = skipType(src, i + 1, mode);
        continue;
      }
      out += c;
      prevSig = 'x';
      prevPrevToken = prevToken;
      prevToken = '';
      i++;
      continue;
    }
    if (c === '(') {
      parens++;
    } else if (c === ')') {
      parens--;
    } else if (c === '{') {
      braces++;
    } else if (c === '}') {
      braces--;
    }
    if (!/\s/.test(c)) {
      prevSig = c === ')' ? ')' : 'x';
      prevPrevToken = prevToken;
      prevToken = '';
    }
    out += c;
    i++;
  }
  return out;
}

function stripFile(src) {
  return stripAnnotations(rewriteModules(stripTypeBlocks(src)));
}

function main() {
  const srcDir = path.join(__dirname, '..', 'src');
  const outDir = path.join(__dirname, '..', '.build');
  fs.rmSync(outDir, { recursive: true, force: true });
  fs.mkdirSync(outDir, { recursive: true });
  for (const f of fs.readdirSync(srcDir)) {
    if (!f.endsWith('.ts')) {
      continue;
    }
    const js = stripFile(fs.readFileSync(path.join(srcDir, f), 'utf8'));
    new vm.Script(js, { filename: f });
    fs.writeFileSync(path.join(outDir, f.replace(/\.ts$/, '.js')), js);
    console.log('stripped', f);
  }
}

main();
