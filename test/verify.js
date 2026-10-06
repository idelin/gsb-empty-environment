// test/verify.js — deterministic cases plus property tests that cross-check
// the offset/edit-based implementation (src/editor.ts) against the
// independent line-by-line implementation (src/reference.ts), with a focus
// on overlapping selections and mixed whitespace. Run: npm test
'use strict';
const assert = require('assert');
const editor = require('../.build/editor');
const reference = require('../.build/reference');

const S = (anchor, caret) => ({ anchor, caret });
const st = (text, sels) => ({ text, selections: sels });

let count = 0;
function test(name, fn) {
  fn();
  count++;
  console.log('ok -', name);
}

function assertStateEq(actual, expected, what) {
  assert.strictEqual(actual.text, expected.text, what + ': text');
  assert.deepStrictEqual(actual.selections, expected.selections, what + ': selections');
}

// ---------- deterministic cases ----------

test('caret tab jumps to the next 4-column tab stop', () => {
  const r1 = editor.insertTab(st('ab', [S(2, 2)]), 4);
  assertStateEq(r1.state, st('ab  ', [S(4, 4)]), 'plain');
  const r2 = editor.insertTab(st('\t', [S(1, 1)]), 4);
  assertStateEq(r2.state, st('\t    ', [S(5, 5)]), 'after hard tab');
  const r3 = editor.insertTab(st('a\tb', [S(2, 2)]), 4);
  assertStateEq(r3.state, st('a\t    b', [S(6, 6)]), 'tab inside prefix');
  const r4 = editor.insertTab(st(' \t', [S(2, 2)]), 4);
  assertStateEq(r4.state, st(' \t    ', [S(6, 6)]), 'space+tab prefix');
  const r5 = editor.insertTab(st('abc', [S(3, 3)]), 4);
  assertStateEq(r5.state, st('abc ', [S(4, 4)]), 'column 3');
});

test('multiple carets on one line each get their own tab stop', () => {
  const r = editor.insertTab(st('axb', [S(1, 1), S(2, 2)]), 4);
  assertStateEq(r.state, st('a   x  b', [S(4, 4), S(7, 7)]), 'two carets');
});

test('duplicate carets insert only once', () => {
  const r = editor.insertTab(st('xy', [S(1, 1), S(1, 1)]), 4);
  assertStateEq(r.state, st('x   y', [S(4, 4), S(4, 4)]), 'dedup');
});

test('any non-caret selection switches tab to line indent', () => {
  const r = editor.insertTab(st('ab\ncd', [S(1, 1), S(4, 5)]), 4);
  assert.strictEqual(r.state.text, '    ab\n    cd');
});

test('indent aligns existing indent to the next tab stop', () => {
  assert.strictEqual(editor.indentLines(st('x', [S(0, 0)]), 4).state.text, '    x');
  assert.strictEqual(editor.indentLines(st('  x', [S(0, 0)]), 4).state.text, '    x');
  assert.strictEqual(editor.indentLines(st('    x', [S(0, 0)]), 4).state.text, '        x');
  assert.strictEqual(editor.indentLines(st('\tx', [S(0, 0)]), 4).state.text, '    \tx');
  assert.strictEqual(editor.indentLines(st(' \t x', [S(0, 0)]), 4).state.text, '    \t x');
});

test('outdent removes at most one tab stop, tabs counted visually', () => {
  assert.strictEqual(editor.outdentLines(st('     y', [S(0, 0)]), 4).state.text, ' y');
  assert.strictEqual(editor.outdentLines(st('    y', [S(0, 0)]), 4).state.text, 'y');
  assert.strictEqual(editor.outdentLines(st('  \tx', [S(0, 0)]), 4).state.text, 'x');
  assert.strictEqual(editor.outdentLines(st(' \tx', [S(0, 0)]), 4).state.text, 'x');
  assert.strictEqual(editor.outdentLines(st('\t\tx', [S(0, 0)]), 4).state.text, '\tx');
  assert.strictEqual(editor.outdentLines(st('\t \tx', [S(0, 0)]), 4).state.text, ' \tx');
  assert.strictEqual(editor.outdentLines(st('  \t\tx', [S(0, 0)]), 4).state.text, '\tx');
  assert.strictEqual(editor.outdentLines(st('x', [S(0, 0)]), 4).state.text, 'x');
});

test('several selections sharing a line indent it exactly once', () => {
  const r = editor.indentLines(st('ab', [S(0, 1), S(1, 2), S(0, 2)]), 4);
  assert.strictEqual(r.state.text, '    ab');
  const r2 = editor.outdentLines(st('\tab', [S(0, 1), S(1, 2), S(0, 2)]), 4);
  assert.strictEqual(r2.state.text, 'ab');
});

test('selection ending exactly at a line start does not touch that line', () => {
  assert.strictEqual(editor.indentLines(st('aa\nbb', [S(0, 3)]), 4).state.text, '    aa\nbb');
  assert.strictEqual(editor.indentLines(st('aa\nbb', [S(0, 4)]), 4).state.text, '    aa\n    bb');
});

test('caret exactly at line start stays at column 0 on indent', () => {
  const r = editor.indentLines(st('ab\ncd', [S(3, 3)]), 4);
  assertStateEq(r.state, st('ab\n    cd', [S(3, 3)]), 'sticky start');
});

test('caret inside removed whitespace clamps to the line start', () => {
  const r = editor.outdentLines(st('  \tx', [S(2, 2)]), 4);
  assertStateEq(r.state, st('x', [S(0, 0)]), 'clamped');
});

test('backward selections keep their orientation', () => {
  const r = editor.indentLines(st('abcd', [S(3, 1)]), 4);
  assert.deepStrictEqual(r.state.selections, [S(7, 5)]);
});

test('selections track their text through indent and outdent', () => {
  const r = editor.indentLines(st('ab\n cd', [S(0, 0), S(4, 4)]), 4);
  assertStateEq(r.state, st('    ab\n    cd', [S(0, 0), S(11, 11)]), 'indent');
  const r2 = editor.outdentLines(st('    ab\n    cd', [S(5, 5), S(12, 12)]), 4);
  assertStateEq(r2.state, st('ab\ncd', [S(1, 1), S(4, 4)]), 'outdent');
});

test('empty document and empty selection list are stable', () => {
  assert.strictEqual(editor.outdentLines(st('', []), 4).state.text, '');
  assert.strictEqual(editor.insertTab(st('', []), 4).state.text, '');
  const r = editor.insertTab(st('', [S(0, 0)]), 4);
  assertStateEq(r.state, st('    ', [S(4, 4)]), 'tab in empty doc');
});

test('undo restores the previous bytes and selections exactly', () => {
  const before = st('  \tfoo\n\tbar \t\n\n    x', [S(1, 5), S(9, 9), S(20, 14)]);
  for (const op of [editor.insertTab, editor.indentLines, editor.outdentLines]) {
    const r = op(before, 4);
    const back = editor.undo(r.state, r.record);
    assertStateEq(back, before, 'undo');
    const fwd = editor.redo(back, r.record);
    assertStateEq(fwd, r.state, 'redo');
  }
});

// ---------- property tests ----------

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

const LEADS = ['', ' ', '  ', '   ', '    ', '\t', '\t\t', ' \t', '\t ', ' \t ', '  \t', '\t \t', '\t  '];
const BODIES = ['x', 'foo();', '', 'a b', '}', 'if (a) {', 'mid\tinner', 'return 1;', '    ', '\t', '  x  '];

function genDoc(rand) {
  const n = 1 + Math.floor(rand() * 6);
  const lines = [];
  for (let i = 0; i < n; i++) {
    lines.push(LEADS[(rand() * LEADS.length) | 0] + BODIES[(rand() * BODIES.length) | 0]);
  }
  let text = lines.join('\n');
  if (rand() < 0.2) {
    text += '\n';
  }
  return text;
}

function genSelections(rand, len) {
  const n = 1 + Math.floor(rand() * 4);
  const sels = [];
  for (let i = 0; i < n; i++) {
    sels.push({ anchor: (rand() * (len + 1)) | 0, caret: (rand() * (len + 1)) | 0 });
  }
  return sels;
}

function visualColOf(prefix) {
  let col = 0;
  for (const ch of prefix) {
    col = ch === '\t' ? col + (4 - (col % 4)) : col + 1;
  }
  return col;
}

const OPS = [
  { name: 'insertTab', main: (s) => editor.insertTab(s, 4).state, ref: (s) => reference.refInsertTab(s, 4) },
  { name: 'indentLines', main: (s) => editor.indentLines(s, 4).state, ref: (s) => reference.refIndentLines(s, 4) },
  { name: 'outdentLines', main: (s) => editor.outdentLines(s, 4).state, ref: (s) => reference.refOutdentLines(s, 4) },
];

const ITERS = 3000;
test('property: main vs independent line-by-line implementation', () => {
  for (let iter = 0; iter < ITERS; iter++) {
    const rand = mulberry32(iter);
    const text = genDoc(rand);
    const selections = genSelections(rand, text.length);
    const state = st(text, selections);
    for (const op of OPS) {
      const where = 'iter ' + iter + ' ' + op.name + ' doc=' + JSON.stringify(text) + ' sels=' + JSON.stringify(selections);
      assertStateEq(op.main(state), op.ref(state), where);
    }
  }
});

test('property: undo restores bytes, redo reapplies them', () => {
  for (let iter = 0; iter < ITERS; iter++) {
    const rand = mulberry32(iter ^ 0x9e3779b9);
    const state = st(genDoc(rand), []);
    state.selections = genSelections(rand, state.text.length);
    for (const run of [editor.insertTab, editor.indentLines, editor.outdentLines]) {
      const r = run(state, 4);
      const where = 'iter ' + iter + ' doc=' + JSON.stringify(state.text);
      assertStateEq(editor.undo(r.state, r.record), state, where + ' undo');
      assertStateEq(editor.redo(editor.undo(r.state, r.record), r.record), r.state, where + ' redo');
    }
  }
});

test('property: duplicating selections never changes the resulting text', () => {
  for (let iter = 0; iter < ITERS; iter++) {
    const rand = mulberry32(iter ^ 0x51f15eed);
    const text = genDoc(rand);
    const selections = genSelections(rand, text.length);
    const dup = st(text, selections.concat(selections));
    const single = st(text, selections);
    assert.strictEqual(editor.indentLines(dup, 4).state.text, editor.indentLines(single, 4).state.text, 'indent iter ' + iter);
    assert.strictEqual(editor.outdentLines(dup, 4).state.text, editor.outdentLines(single, 4).state.text, 'outdent iter ' + iter);
  }
});

test('property: caret tab always lands on a tab stop; lengths monotone', () => {
  for (let iter = 0; iter < ITERS; iter++) {
    const rand = mulberry32(iter ^ 0x2c1b3c4d);
    const text = genDoc(rand);
    const selections = genSelections(rand, text.length);
    const state = st(text, selections);
    if (selections.every((s) => s.anchor === s.caret)) {
      const r = editor.insertTab(state, 4).state;
      for (const sel of r.selections) {
        const lineStart = r.text.lastIndexOf('\n', sel.caret - 1) + 1;
        assert.strictEqual(visualColOf(r.text.slice(lineStart, sel.caret)) % 4, 0, 'caret off tab stop, iter ' + iter);
      }
    }
    assert.ok(editor.indentLines(state, 4).state.text.length >= text.length, 'indent shrank, iter ' + iter);
    assert.ok(editor.outdentLines(state, 4).state.text.length <= text.length, 'outdent grew, iter ' + iter);
  }
});

console.log('\n' + count + ' test groups passed (' + ITERS + ' property iterations each)');
