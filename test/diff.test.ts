import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LineDiffSession, splitLines, joinLines } from '../src/index.ts';

test('identical texts produce no blocks', () => {
  const s = new LineDiffSession('a\nb\nc\n', 'a\nb\nc\n');
  assert.deepEqual(s.getBlocks(), []);
});

test('insertion: surrounding lines map shifted, inserted lines map to null', () => {
  const s = new LineDiffSession('a\nb\nc\n', 'a\nx\ny\nb\nc\n');
  assert.deepEqual(s.getBlocks(), [
    { kind: 'insert', left: { start: 1, end: 1 }, right: { start: 1, end: 3 } },
  ]);
  assert.equal(s.mapLine('left', 0), 0);
  assert.equal(s.mapLine('left', 1), 3);
  assert.equal(s.mapLine('left', 2), 4);
  assert.equal(s.mapLine('right', 0), 0);
  assert.equal(s.mapLine('right', 1), null);
  assert.equal(s.mapLine('right', 2), null);
  assert.equal(s.mapLine('right', 3), 1);
  assert.equal(s.mapLine('right', 4), 2);
});

test('deletion: deleted lines map to null, never to a neighbour', () => {
  const s = new LineDiffSession('a\nd1\nd2\nb\n', 'a\nb\n');
  assert.deepEqual(s.getBlocks(), [
    { kind: 'delete', left: { start: 1, end: 3 }, right: { start: 1, end: 1 } },
  ]);
  assert.equal(s.mapLine('left', 1), null);
  assert.equal(s.mapLine('left', 2), null);
  assert.equal(s.mapLine('left', 3), 1);
  assert.equal(s.mapLine('right', 1), 3);
});

test('replace: both sides of the block map to null', () => {
  const s = new LineDiffSession('a\nx\nb\n', 'a\ny\nz\nb\n');
  assert.deepEqual(s.getBlocks(), [
    { kind: 'replace', left: { start: 1, end: 2 }, right: { start: 1, end: 3 } },
  ]);
  assert.equal(s.mapLine('left', 1), null);
  assert.equal(s.mapLine('right', 1), null);
  assert.equal(s.mapLine('right', 2), null);
  assert.equal(s.mapLine('left', 2), 3);
  assert.equal(s.mapLine('right', 3), 2);
});

test('repeated lines: deterministic tie-break attributes change to the lowest position', () => {
  const left = 'a\nx\na\nx\n';
  const right = 'a\nx\na\nx\na\nx\n';
  const first = new LineDiffSession(left, right).getBlocks();
  const second = new LineDiffSession(left, right).getBlocks();
  assert.deepEqual(first, second);
  assert.deepEqual(first, [
    { kind: 'insert', left: { start: 4, end: 4 }, right: { start: 4, end: 6 } },
  ]);
  const del = new LineDiffSession(right, left).getBlocks();
  assert.deepEqual(del, [
    { kind: 'delete', left: { start: 4, end: 6 }, right: { start: 4, end: 4 } },
  ]);
});

test('repeated empty lines: stable diff, deletion slid down next to the anchor', () => {
  const left = 'a\n\n\n\nb\n'; // a, '', '', '', b
  const right = 'a\n\n\nb\n'; // a, '', '', b
  const blocks = new LineDiffSession(left, right).getBlocks();
  assert.deepEqual(blocks, [
    { kind: 'delete', left: { start: 3, end: 4 }, right: { start: 3, end: 3 } },
  ]);
  // The deleted line maps to null; the identical empty lines around it map fine.
  const s = new LineDiffSession(left, right);
  assert.equal(s.mapLine('left', 1), 1);
  assert.equal(s.mapLine('left', 2), 2);
  assert.equal(s.mapLine('left', 3), null);
  assert.equal(s.mapLine('left', 4), 3);
});

test('many repeated empty lines: stable across runs, only empty lines deleted', () => {
  const left = 'a\n\n\nb\n\n\nc\n';
  const right = 'a\n\nb\n\nc\n';
  const run1 = new LineDiffSession(left, right).getBlocks();
  const run2 = new LineDiffSession(left, right).getBlocks();
  assert.deepEqual(run1, run2);
  assert.equal(run1.length, 2);
  let deleted = 0;
  for (const b of run1) {
    assert.equal(b.kind, 'delete');
    for (let i = b.left.start; i < b.left.end; i++) {
      assert.equal(new LineDiffSession(left, right).getLine('left', i).text, '');
      deleted++;
    }
  }
  assert.equal(deleted, 2);
  // Self-diff of heavily repeated content is empty.
  assert.deepEqual(new LineDiffSession(left, left).getBlocks(), []);
});

test('line splitting round-trips LF, CRLF, lone CR, mixed and missing final EOL', () => {
  const samples = [
    '',
    '\n',
    'a',
    'a\n',
    'a\nb',
    'a\r\nb\r\n',
    'a\rb\rc',
    'a\r\nb\nc\rd\r\n',
    '\r\n\r\n',
    'x\n\n\ny',
  ];
  for (const t of samples) {
    assert.equal(joinLines(splitLines(t)), t, JSON.stringify(t));
  }
  assert.equal(splitLines('a\r\nb\r\n').length, 2);
  assert.equal(splitLines('').length, 0);
  assert.deepEqual(
    splitLines('a\nb').map((l) => l.eol),
    ['LF', 'NONE'],
  );
});

test('LF and CRLF compare by content; terminator style is preserved per side', () => {
  const s = new LineDiffSession('a\r\nb\r\n', 'a\nb\nc\n');
  assert.deepEqual(s.getBlocks(), [
    { kind: 'insert', left: { start: 2, end: 2 }, right: { start: 2, end: 3 } },
  ]);
  assert.equal(s.getLine('left', 0).eol, 'CRLF');
  assert.equal(s.getLine('right', 0).eol, 'LF');
  assert.equal(s.mapLine('left', 1), 1);
});

test('UTF-16: surrogate pairs are never split by offsets or edits', () => {
  // '😀' = 😀 (2 UTF-16 code units)
  const s = new LineDiffSession('😀ab\n', '😀ab\n');
  assert.equal(s.lineColToOffset('left', 0, 1), 0); // snapped back to pair start
  assert.equal(s.lineColToOffset('left', 0, 2), 2);
  assert.deepEqual(s.offsetToLineCol('left', 1), { line: 0, col: 0 });
  assert.deepEqual(s.offsetToLineCol('left', 2), { line: 0, col: 2 });

  // Edit range [1, 2) would split '😀'; it expands to [0, 2).
  const applied = s.applyEdit('left', 1, 2, '🎉');
  assert.deepEqual(applied, { start: 0, end: 2 });
  assert.equal(s.getText('left'), '🎉ab\n');
  assert.deepEqual(s.getBlocks(), [
    { kind: 'replace', left: { start: 0, end: 1 }, right: { start: 0, end: 1 } },
  ]);
});

test('UTF-16: offsets inside CRLF terminators resolve to line content end', () => {
  const s = new LineDiffSession('ab\r\ncd\n', 'ab\r\ncd\n');
  assert.deepEqual(s.offsetToLineCol('left', 3), { line: 0, col: 2 });
  assert.deepEqual(s.offsetToLineCol('left', 4), { line: 1, col: 0 });
  assert.equal(s.lineColToOffset('left', 1, 1), 5);
});

test('local edit updates blocks; reverting removes them and folding still maps', () => {
  const text = Array.from({ length: 20 }, (_, i) => `l${i}`).join('\n') + '\n';
  const s = new LineDiffSession(text, text);
  assert.deepEqual(s.getBlocks(), []);

  const insertAt = s.lineColToOffset('right', 10, 0);
  s.applyEdit('right', insertAt, insertAt, 'new1\nnew2\n');
  assert.deepEqual(s.getBlocks(), [
    { kind: 'insert', left: { start: 10, end: 10 }, right: { start: 10, end: 12 } },
  ]);
  assert.equal(s.mapLine('left', 10), 12);
  assert.equal(s.mapLine('right', 10), null);

  // Revert: remove exactly what was inserted.
  s.applyEdit('right', insertAt, insertAt + 'new1\nnew2\n'.length, '');
  assert.equal(s.getText('right'), text);
  assert.deepEqual(s.getBlocks(), []);

  const view = s.getView(3);
  assert.deepEqual(view, [
    {
      type: 'fold',
      left: { start: 0, end: 20 },
      right: { start: 0, end: 20 },
      lineCount: 20,
    },
  ]);
  assert.equal(s.mapLine('left', 5), 5);
  assert.equal(s.mapLine('right', 19), 19);
});

test('folded view collapses context around a change in a long text', () => {
  const leftLines = Array.from({ length: 30 }, (_, i) => `line${i}`);
  const rightLines = [...leftLines];
  rightLines[15] = 'changed';
  const s = new LineDiffSession(leftLines.join('\n') + '\n', rightLines.join('\n') + '\n');
  const view = s.getView(2);
  assert.deepEqual(view, [
    { type: 'fold', left: { start: 0, end: 13 }, right: { start: 0, end: 13 }, lineCount: 13 },
    { type: 'equal', left: { start: 13, end: 15 }, right: { start: 13, end: 15 } },
    {
      type: 'change',
      block: { kind: 'replace', left: { start: 15, end: 16 }, right: { start: 15, end: 16 } },
    },
    { type: 'equal', left: { start: 16, end: 18 }, right: { start: 16, end: 18 } },
    { type: 'fold', left: { start: 18, end: 30 }, right: { start: 18, end: 30 }, lineCount: 12 },
  ]);
  // Mapping works straight through folded regions.
  assert.equal(s.mapLine('left', 0), 0);
  assert.equal(s.mapLine('left', 12), 12);
  assert.equal(s.mapLine('left', 29), 29);
  assert.equal(s.mapLine('left', 15), null);
});

test('empty texts and one-sided content', () => {
  assert.deepEqual(new LineDiffSession('', '').getBlocks(), []);
  assert.deepEqual(new LineDiffSession('', '').getView(3), []);

  const ins = new LineDiffSession('', 'a\n');
  assert.deepEqual(ins.getBlocks(), [
    { kind: 'insert', left: { start: 0, end: 0 }, right: { start: 0, end: 1 } },
  ]);
  assert.equal(ins.mapLine('right', 0), null);

  const del = new LineDiffSession('a\n', '');
  assert.deepEqual(del.getBlocks(), [
    { kind: 'delete', left: { start: 0, end: 1 }, right: { start: 0, end: 0 } },
  ]);
  assert.equal(del.mapLine('left', 0), null);
  assert.throws(() => del.mapLine('right', 0), RangeError);
});

test('line/offset round-trip across every line of a mixed-EOL document', () => {
  const text = 'one\r\ntwo\nthree\r\n😀 four\nlast';
  const s = new LineDiffSession(text, text);
  for (let line = 0; line < s.getLineCount('left'); line++) {
    const l = s.getLine('left', line);
    for (let col = 0; col <= l.text.length; col++) {
      const offset = s.lineColToOffset('left', line, col);
      const pos = s.offsetToLineCol('left', offset);
      assert.equal(pos.line, line);
      // col may have been snapped back off a surrogate boundary
      assert.ok(Math.abs(pos.col - col) <= 1);
    }
  }
});

test('long text: distant small edits stay separate small blocks; revert restores', () => {
  const base = Array.from({ length: 20000 }, (_, i) => `line ${i}`).join('\n') + '\n';
  const other = base
    .replace('line 10000\n', 'line 10000\ninserted A\ninserted B\n')
    .replace('line 15000\n', '');
  const s = new LineDiffSession(base, other);
  const expected = [
    { kind: 'insert', left: { start: 10001, end: 10001 }, right: { start: 10001, end: 10003 } },
    { kind: 'delete', left: { start: 15000, end: 15001 }, right: { start: 15002, end: 15002 } },
  ];
  assert.deepEqual(s.getBlocks(), expected);
  assert.equal(s.mapLine('left', 19999), 20000);
  assert.equal(s.mapLine('right', 10001), null);

  // Local edit on one side, then revert: original blocks come back.
  const off = s.lineColToOffset('left', 5000, 0);
  s.applyEdit('left', off, off, 'local edit\n');
  assert.equal(s.getBlocks().length, 3);
  s.applyEdit('left', off, off + 'local edit\n'.length, '');
  assert.deepEqual(s.getBlocks(), expected);
});
