import { strict as assert } from 'node:assert';
import { rmSync, mkdirSync, writeFileSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { stripTypes } from '../scripts/strip-types.mjs';

const rootDir = join(dirname(fileURLToPath(import.meta.url)), '..');
const buildDir = join(rootDir, '.build');
rmSync(buildDir, { recursive: true, force: true });
mkdirSync(buildDir, { recursive: true });
for (const name of ['parse', 'enumerate', 'vectors', 'search', 'index']) {
  const source = readFileSync(join(rootDir, 'src', `${name}.ts`), 'utf8');
  writeFileSync(join(buildDir, `${name}.js`), stripTypes(source));
}
const lib = await import(pathToFileURL(join(buildDir, 'index.js')).href);

let passed = 0;
const failures = [];
function test(name, fn) {
  try {
    fn();
    passed++;
  } catch (err) {
    failures.push({ name, err });
  }
}

function expectSearch(pattern, input, expected) {
  const result = lib.searchPattern(pattern, input);
  const label = `/${pattern}/ on ${JSON.stringify(input)}`;
  if (expected === null) {
    assert.equal(result, null, `${label}: expected no match`);
    return;
  }
  assert.ok(result !== null, `${label}: expected a match`);
  assert.equal(result.start, expected.start, `${label}: start`);
  assert.equal(result.end, expected.end, `${label}: end`);
  assert.equal(result.match, input.slice(expected.start, expected.end), `${label}: match`);
  assert.deepEqual([...result.groups], expected.groups, `${label}: groups`);
}

// ---------- parser ----------
test('parser: group numbering follows opening parens', () => {
  assert.equal(lib.parsePattern('((a)|(b))').groupCount, 3);
  assert.equal(lib.parsePattern('').groupCount, 0);
  assert.equal(lib.parsePattern('(a(b)c)').groupCount, 2);
});

test('parser: rejects invalid patterns', () => {
  for (const bad of ['(', ')', '*a', 'a\\', '(a', 'a)']) {
    assert.throws(() => lib.parsePattern(bad), SyntaxError, bad);
  }
});

test('parser: accepts empty alternatives and empty groups', () => {
  for (const ok of ['', '()', 'a|', '|a', '(|)', 'a**', '()*']) {
    lib.parsePattern(ok);
  }
});

// ---------- semantics: table-driven ----------
const cases = [
  ['a', 'a', { start: 0, end: 1, groups: [] }],
  ['a', 'ba', { start: 1, end: 2, groups: [] }],
  ['a', 'b', null],
  ['abc', 'zabcy', { start: 1, end: 4, groups: [] }],
  ['a|bc', 'bc', { start: 0, end: 2, groups: [] }],
  ['a|aa', 'aa', { start: 0, end: 2, groups: [] }],
  ['aa|a', 'aa', { start: 0, end: 2, groups: [] }],
  ['b|ab', 'ab', { start: 0, end: 2, groups: [] }],
  ['a*', 'aaa', { start: 0, end: 3, groups: [] }],
  ['a*', 'b', { start: 0, end: 0, groups: [] }],
  ['a*', 'aab', { start: 0, end: 2, groups: [] }],
  ['', 'ab', { start: 0, end: 0, groups: [] }],
  ['(a)', 'a', { start: 0, end: 1, groups: ['a'] }],
  ['()', 'a', { start: 0, end: 0, groups: [''] }],
  ['(a)|(b)', 'a', { start: 0, end: 1, groups: ['a', undefined] }],
  ['(a)|(b)', 'b', { start: 0, end: 1, groups: [undefined, 'b'] }],
  ['(a*)', 'b', { start: 0, end: 0, groups: [''] }],
  ['(a|)', 'b', { start: 0, end: 0, groups: [''] }],
  ['(|a)', 'a', { start: 0, end: 1, groups: ['a'] }],
  ['(a|aa)(a|)', 'aa', { start: 0, end: 2, groups: ['aa', ''] }],
  ['(a*)(a*)', 'aa', { start: 0, end: 2, groups: ['aa', ''] }],
  ['((a)(b))*', 'abab', { start: 0, end: 4, groups: ['ab', 'a', 'b'] }],
  ['(a(b))*', 'abab', { start: 0, end: 4, groups: ['ab', 'b'] }],
  ['(a(b)|a)*', 'aba', { start: 0, end: 3, groups: ['a', 'b'] }],
  ['(a*)*', 'aa', { start: 0, end: 2, groups: ['aa'] }],
  ['(a*)*', '', { start: 0, end: 0, groups: [''] }],
  ['((a*)*)*', 'aaa', { start: 0, end: 3, groups: ['aaa', 'aaa'] }],
  ['(a|)*', 'a', { start: 0, end: 1, groups: ['a'] }],
  ['()*', 'ab', { start: 0, end: 0, groups: [''] }],
  ['a**', 'aa', { start: 0, end: 2, groups: [] }],
  ['(b|)a', 'ba', { start: 0, end: 2, groups: ['b'] }],
  ['x|ab', 'zab', { start: 1, end: 3, groups: [] }],
  ['\\*', '*', { start: 0, end: 1, groups: [] }],
  ['\\(a\\)', '(a)', { start: 0, end: 3, groups: [] }],
  ['\\\\', '\\', { start: 0, end: 1, groups: [] }],
  ['level=(a|b)*', 'x level=abba y', { start: 2, end: 12, groups: ['a'] }],
];
for (const [pattern, input, expected] of cases) {
  test(`search: /${pattern}/ on ${JSON.stringify(input)}`, () => {
    expectSearch(pattern, input, expected);
  });
}

test('non-participating group is undefined, not empty string', () => {
  const result = lib.searchPattern('(a)|(b)', 'b');
  assert.ok(result !== null);
  assert.ok(!('0' in result.groups) || result.groups[0] === undefined);
  assert.strictEqual(result.groups[0], undefined);
  assert.strictEqual(result.groups[1], 'b');
  const empty = lib.searchPattern('(a*)', 'b');
  assert.ok(empty !== null);
  assert.strictEqual(empty.groups[0], '');
  assert.notStrictEqual(empty.groups[0], undefined);
});

// ---------- independent reference implementations ----------
function possibleEnds(ast, input, pos) {
  switch (ast.kind) {
    case 'lit': {
      return pos < input.length && input[pos] === ast.ch ? new Set([pos + 1]) : new Set();
    }
    case 'concat': {
      let ends = new Set([pos]);
      for (const part of ast.parts) {
        const next = new Set();
        for (const from of ends) {
          for (const to of possibleEnds(part, input, from)) next.add(to);
        }
        ends = next;
      }
      return ends;
    }
    case 'alt': {
      const ends = new Set();
      for (const option of ast.options) {
        for (const end of possibleEnds(option, input, pos)) ends.add(end);
      }
      return ends;
    }
    case 'star': {
      const reach = new Set([pos]);
      const queue = [pos];
      while (queue.length > 0) {
        const from = queue.pop();
        for (const to of possibleEnds(ast.inner, input, from)) {
          if (to !== from && !reach.has(to)) {
            reach.add(to);
            queue.push(to);
          }
        }
      }
      return reach;
    }
    case 'group': {
      return possibleEnds(ast.inner, input, pos);
    }
    default:
      throw new Error(`unknown kind ${ast.kind}`);
  }
}

function refRank(span) {
  if (span === undefined) return 0;
  return span[1] - span[0] + 1;
}

function refCompare(a, b) {
  if (a.end !== b.end) return b.end - a.end;
  for (let i = 0; i < a.caps.length; i++) {
    const ra = refRank(a.caps[i]);
    const rb = refRank(b.caps[i]);
    if (ra !== rb) return rb - ra;
  }
  return 0;
}

const TREE_CAP = 300000;

function referenceSearch(ast, groupCount, input) {
  for (let start = 0; start <= input.length; start++) {
    let best = null;
    let count = 0;
    for (const tree of lib.enumerateTrees(ast, input, start)) {
      count++;
      if (count > TREE_CAP) return 'CAP';
      const vector = { end: tree.end, caps: lib.treeCaptures(tree, groupCount) };
      if (best === null || refCompare(vector, best) < 0) best = vector;
    }
    if (best !== null) return { start, end: best.end, caps: best.caps };
  }
  return null;
}

function sortedNumbers(set) {
  return [...set].sort((x, y) => x - y);
}

function crossCheck(pattern, input) {
  const { ast, groupCount } = lib.parsePattern(pattern);
  for (let start = 0; start <= input.length; start++) {
    const treeEnds = new Set();
    let count = 0;
    for (const tree of lib.enumerateTrees(ast, input, start)) {
      treeEnds.add(tree.end);
      count++;
      if (count > TREE_CAP) break;
    }
    const expected = sortedNumbers(possibleEnds(ast, input, start));
    if (count <= TREE_CAP) {
      assert.deepEqual(sortedNumbers(treeEnds), expected, `tree ends /${pattern}/ @${start}`);
    }
    const vectorEnds = new Set(lib.enumMatchVectors(ast, input, start, groupCount).map((v) => v.end));
    assert.deepEqual(sortedNumbers(vectorEnds), expected, `vector ends /${pattern}/ @${start}`);
  }
  const expected = referenceSearch(ast, groupCount, input);
  if (expected === 'CAP') return 'CAP';
  const actual = lib.searchAst(ast, groupCount, input);
  const label = `/${pattern}/ on ${JSON.stringify(input)}`;
  if (expected === null) {
    assert.equal(actual, null, label);
    return 'OK';
  }
  assert.ok(actual !== null, `${label}: expected a match`);
  assert.equal(actual.start, expected.start, `${label}: start`);
  assert.equal(actual.end, expected.end, `${label}: end`);
  const expectedGroups = expected.caps.map((s) => (s === undefined ? undefined : input.slice(s[0], s[1])));
  assert.deepEqual([...actual.groups], expectedGroups, `${label}: groups`);
  return 'OK';
}

// ---------- cross-validation on curated patterns ----------
const curatedPatterns = [
  'a', 'ab', 'a|b', 'a*', '(a)', '(a|b)', '(a*)(b*)', '(a|b)*', '(a*)*',
  '((a)(b))*', '(a(b)|a)*', '((a*)*)*', '(a|)*', '()*', '(a|aa)(a|)',
  'a(b|)(c|)', '(ab|a)(b|)', '((a)|(b))*', '(a*)(a*)(a*)',
];
const curatedInputs = ['', 'a', 'b', 'aa', 'ab', 'ba', 'bb', 'aab', 'aba', 'abb', 'baa', 'abab', 'aaab', 'aabb'];
for (const pattern of curatedPatterns) {
  for (const input of curatedInputs) {
    test(`cross: /${pattern}/ on ${JSON.stringify(input)}`, () => {
      crossCheck(pattern, input);
    });
  }
}

// ---------- cross-validation on random patterns ----------
function mulberry32(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function genPattern(rand, depth) {
  const roll = rand();
  if (depth <= 0 || roll < 0.35) {
    const lits = ['a', 'b', 'c', ''];
    return lits[Math.floor(rand() * lits.length)];
  }
  const kind = Math.floor(rand() * 5);
  if (kind === 0 || kind === 4) return genPattern(rand, depth - 1) + genPattern(rand, depth - 1);
  if (kind === 1) return genPattern(rand, depth - 1) + '|' + genPattern(rand, depth - 1);
  if (kind === 2) return '(' + genPattern(rand, depth - 1) + ')*';
  return '(' + genPattern(rand, depth - 1) + ')';
}

function genInput(rand, maxLen) {
  const len = Math.floor(rand() * (maxLen + 1));
  let out = '';
  for (let i = 0; i < len; i++) out += 'abc'[Math.floor(rand() * 3)];
  return out;
}

function exhaustiveInputs(alphabet, maxLen) {
  const out = [''];
  let level = [''];
  for (let len = 1; len <= maxLen; len++) {
    const next = [];
    for (const prefix of level) {
      for (const ch of alphabet) {
        next.push(prefix + ch);
        out.push(prefix + ch);
      }
    }
    level = next;
  }
  return out;
}

test('random patterns: search matches parse-tree reference', () => {
  const rand = mulberry32(20261006);
  const smallInputs = exhaustiveInputs('ab', 4);
  let caps = 0;
  let checks = 0;
  for (let p = 0; p < 150; p++) {
    const pattern = genPattern(rand, 3);
    const inputs = [...smallInputs];
    for (let k = 0; k < 12; k++) inputs.push(genInput(rand, 6));
    for (const input of inputs) {
      const status = crossCheck(pattern, input);
      if (status === 'CAP') caps++;
      else checks++;
    }
  }
  assert.ok(checks > 5000, `too few effective checks: ${checks}`);
  assert.ok(caps < 50, `too many capped cases: ${caps}`);
});

// ---------- termination on length-20 inputs ----------
const terminationCases = [
  ['(a*)*', 'a'.repeat(20)],
  ['((a*)*)*', 'a'.repeat(20)],
  ['((((a*)*)*)*)*', 'a'.repeat(20)],
  ['(a|)*', 'a'.repeat(20)],
  ['()*', 'a'.repeat(20)],
  ['(a|a|a|a|a)*', 'a'.repeat(20)],
  ['(a|aa)*', 'a'.repeat(20)],
  ['(a*)(b*)', 'a'.repeat(10) + 'b'.repeat(10)],
  ['(a|b)*abb', 'a'.repeat(17) + 'abb'],
  ['(|a)(|a)(|a)', 'a'.repeat(20)],
];
for (const [pattern, input] of terminationCases) {
  test(`terminates: /${pattern}/ on length ${input.length}`, () => {
    const started = Date.now();
    const result = lib.searchPattern(pattern, input);
    const elapsed = Date.now() - started;
    assert.ok(elapsed < 5000, `took ${elapsed}ms`);
    assert.ok(result !== null);
    assert.equal(result.start, 0);
  });
}

test('length-20 result: (a*)* captures the whole run', () => {
  const input = 'a'.repeat(20);
  const result = lib.searchPattern('(a*)*', input);
  assert.ok(result !== null);
  assert.equal(result.end, 20);
  assert.strictEqual(result.groups[0], input);
});

// ---------- report ----------
console.log(`passed: ${passed}`);
if (failures.length > 0) {
  console.log(`failed: ${failures.length}`);
  for (const { name, err } of failures.slice(0, 10)) {
    console.log(`\nFAIL: ${name}`);
    console.log(err.message);
  }
  process.exitCode = 1;
}
