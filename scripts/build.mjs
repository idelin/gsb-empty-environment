// Offline type-stripper: converts the constrained TypeScript sources in src/
// and test/ into runnable .mjs files under build/. The sources avoid TS
// constructs that cannot be removed line-wise (no generics, no casts, no
// object literals or ternaries inside parentheses, no parens in strings).
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');

const files = [
  ['src/pulse-detector.ts', 'build/src/pulse-detector.mjs'],
  ['test/pulse-detector.test.ts', 'build/test/pulse-detector.test.mjs'],
];

function maskStrings(line) {
  let out = '';
  let inStr = false;
  let quote = '';
  for (const ch of line) {
    if (inStr) {
      if (ch === quote) inStr = false;
      out += ' ';
    } else if (ch === "'" || ch === '"' || ch === '`') {
      inStr = true;
      quote = ch;
      out += ' ';
    } else {
      out += ch;
    }
  }
  return out;
}

function braceDelta(mask) {
  let delta = 0;
  for (const ch of mask) {
    if (ch === '{') delta += 1;
    if (ch === '}') delta -= 1;
  }
  return delta;
}

function parenRegions(mask) {
  const regions = [];
  let depth = 0;
  let start = -1;
  for (let i = 0; i < mask.length; i++) {
    const ch = mask[i];
    if (ch === '(') {
      if (depth === 0) start = i;
      depth += 1;
    } else if (ch === ')') {
      depth -= 1;
      if (depth === 0) regions.push([start, i + 1]);
    }
  }
  return regions;
}

function collectSpans(mask, lineNo) {
  const spans = [];
  const declRes = [
    /^(\s*)(?:(?:private|public|protected|readonly|static)\s+)+([A-Za-z_$][\w$]*)\s*:\s*[^=;]+([=;])/,
    /^(\s*(?:const|let|var)\s+[A-Za-z_$][\w$]*)\s*:\s*[^=;]+([=;])/,
  ];
  for (const re of declRes) {
    const m = re.exec(mask);
    if (m) {
      if (re === declRes[0]) {
        spans.push([m[1].length, m[0].length, m[2] + m[3]]);
      } else {
        spans.push([m.index + m[1].length, m.index + m[0].length - m[2].length, '']);
      }
    }
  }
  const methodRe = /^(\s*)(?:(?:public|private|protected|readonly|static|async)\s+)+([A-Za-z_$][\w$]*\s*\()/;
  const mm = methodRe.exec(mask);
  if (mm) {
    spans.push([mm[1].length, mm[0].length - mm[2].length, '']);
  }
  for (const [rs, re_] of parenRegions(mask)) {
    const content = mask.slice(rs, re_);
    if (content.includes('{') || content.includes('?')) {
      throw new Error('line ' + lineNo + ': object literal or ternary inside parentheses is not supported');
    }
    const paramRe = /([A-Za-z_$][\w$]*)\s*:\s*[^,)=]+/g;
    let pm;
    while ((pm = paramRe.exec(content)) !== null) {
      spans.push([rs + pm.index + pm[1].length, rs + pm.index + pm[0].length, '']);
    }
  }
  const retRe = /\)\s*:\s*[A-Za-z_$][\w$[\]]*(?:\s*\|\s*[A-Za-z_$][\w$[\]]*)*\s*(?=[{=])/g;
  let rm;
  while ((rm = retRe.exec(mask)) !== null) {
    spans.push([rm.index + 1, rm.index + rm[0].length, '']);
  }
  return spans;
}

function stripTypes(source) {
  const outLines = [];
  let interfaceDepth = 0;
  const lines = source.split('\n');
  for (let i = 0; i < lines.length; i++) {
    const rawLine = lines[i];
    const maskedFull = maskStrings(rawLine);
    const commentAt = maskedFull.indexOf('//');
    const mask = commentAt >= 0 ? maskedFull.slice(0, commentAt) : maskedFull;
    const line = commentAt >= 0 ? rawLine.slice(0, commentAt) : rawLine;
    if (interfaceDepth > 0) {
      interfaceDepth += braceDelta(mask);
      continue;
    }
    if (/^\s*(export\s+)?interface\s+[A-Za-z_$]/.test(mask)) {
      interfaceDepth += braceDelta(mask);
      continue;
    }
    if (/^\s*(export\s+)?type\s+[A-Za-z_$]/.test(mask)) continue;
    if (/^\s*import\s+type\s/.test(mask)) continue;
    const spans = collectSpans(mask, i + 1);
    spans.sort((a, b) => b[0] - a[0]);
    let result = line;
    for (const [start, end, insert] of spans) {
      result = result.slice(0, start) + insert + result.slice(end);
    }
    result = result.replace(/\.js(['"])/g, '.mjs$1');
    outLines.push(result);
  }
  return outLines.join('\n');
}

for (const [src, dst] of files) {
  const source = readFileSync(join(root, src), 'utf8');
  const output = stripTypes(source);
  const target = join(root, dst);
  mkdirSync(dirname(target), { recursive: true });
  writeFileSync(target, output);
  console.log('built ' + dst);
}
