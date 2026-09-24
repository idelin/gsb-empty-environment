import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LayoutEngine } from '../src/index.ts';

test('tab expands to the next tab stop (tabWidth 4: "a" + tab reaches column 4)', () => {
  const engine = new LayoutEngine({ text: 'a\tb', tabWidth: 4 });
  // 'b' is at UTF-16 offset 2 and lands on column 4.
  assert.deepEqual(engine.logicalToVisual(2), { line: 0, row: 0, column: 4 });
  // The tab cluster itself starts at offset 1, column 1.
  assert.deepEqual(engine.logicalToVisual(1), { line: 0, row: 0, column: 1 });
  // Columns 1..3 are inside the tab; 'start' policy snaps to the tab start.
  assert.equal(engine.visualToLogical({ line: 0, row: 0, column: 2 }, 'start').offset, 1);
  assert.equal(engine.visualToLogical({ line: 0, row: 0, column: 4 }).offset, 2);
});

test('tab stop advances by full tabWidth from any column', () => {
  const engine = new LayoutEngine({ text: 'abcd\tef', tabWidth: 4 });
  // tab at column 4 -> next stop is 8, so 'e' (offset 5) is at column 8.
  assert.deepEqual(engine.logicalToVisual(5), { line: 0, row: 0, column: 8 });
});

test('wide characters occupy two columns', () => {
  const engine = new LayoutEngine({ text: 'a世b' });
  assert.deepEqual(engine.logicalToVisual(1), { line: 0, row: 0, column: 1 }); // 世
  assert.deepEqual(engine.logicalToVisual(2), { line: 0, row: 0, column: 3 }); // b
});

test('clicking the second column of a wide char with the forward (start) policy lands on the char start', () => {
  const engine = new LayoutEngine({ text: '世x' });
  const hit = engine.visualToLogical({ line: 0, row: 0, column: 1 }, 'start');
  assert.equal(hit.offset, 0); // 世 starts at UTF-16 offset 0
  assert.equal(hit.affinity, 'forward');
  // The alternative policy snaps to the cluster end instead.
  const hitEnd = engine.visualToLogical({ line: 0, row: 0, column: 1 }, 'end');
  assert.equal(hitEnd.offset, 1);
  assert.equal(hitEnd.affinity, 'backward');
});

test('zero-width combining marks share a cell with their base and are never split', () => {
  const engine = new LayoutEngine({ text: 'éx' });
  // e + U+0301 forms one cluster of width 1 at column 0; x sits at column 1.
  assert.deepEqual(engine.logicalToVisual(2), { line: 0, row: 0, column: 1 });
  // Mid-cluster offset (inside the combining sequence) snaps to cluster start.
  assert.deepEqual(engine.logicalToVisual(1), { line: 0, row: 0, column: 0 });
  // Hit testing can never land between base and combining mark.
  assert.equal(engine.visualToLogical({ line: 0, row: 0, column: 0 }).offset, 0);
  assert.equal(engine.visualToLogical({ line: 0, row: 0, column: 1 }).offset, 2);
});

test('a standalone combining mark is zero-width and owns no column', () => {
  const engine = new LayoutEngine({ text: '́x' });
  assert.deepEqual(engine.logicalToVisual(1), { line: 0, row: 0, column: 0 }); // x at column 0
  assert.equal(engine.lineHeight(0), 1);
});

test('surrogate pairs are never split by any public offset', () => {
  const engine = new LayoutEngine({ text: '😀x' }); // 😀 = U+1F600, offsets 0..2
  // Edits that would split the pair are rejected.
  assert.throws(() => engine.insert(1, 'a'), /surrogate pair/);
  assert.throws(() => engine.delete(0, 1), /surrogate pair/);
  // Mid-pair logical offset snaps to the pair start.
  assert.deepEqual(engine.logicalToVisual(1), { line: 0, row: 0, column: 0 });
  // No visual position ever maps to offset 1 (inside the pair).
  for (let column = 0; column <= 4; column++) {
    const { offset } = engine.visualToLogical({ line: 0, row: 0, column });
    assert.notEqual(offset, 1);
  }
  assert.equal(engine.visualToLogical({ line: 0, row: 0, column: 1 }, 'end').offset, 2);
});

test('soft wrap breaks between clusters and reports line height', () => {
  const engine = new LayoutEngine({ text: 'abcdefgh', wrapColumn: 4 });
  assert.equal(engine.lineHeight(0), 2);
  const rows = engine.allRows();
  assert.deepEqual(rows.map((r) => [r.startOffset, r.endOffset, r.width]), [
    [0, 4, 4],
    [4, 8, 4],
  ]);
});

test('wrap boundary has two visual candidates selected by direction preference', () => {
  const engine = new LayoutEngine({ text: 'abcdefgh', wrapColumn: 4 });
  assert.deepEqual(engine.logicalToVisual(4, 'forward'), { line: 0, row: 1, column: 0 });
  assert.deepEqual(engine.logicalToVisual(4, 'backward'), { line: 0, row: 0, column: 4 });
  // Both visual positions map back to the same logical offset, but carry
  // different affinities instead of pretending to be a bijection.
  const fromRowEnd = engine.visualToLogical({ line: 0, row: 0, column: 4 });
  const fromNextRow = engine.visualToLogical({ line: 0, row: 1, column: 0 });
  assert.equal(fromRowEnd.offset, 4);
  assert.equal(fromRowEnd.affinity, 'backward');
  assert.equal(fromNextRow.offset, 4);
  assert.equal(fromNextRow.affinity, 'forward');
});

test('visual -> logical -> visual round trip via affinity', () => {
  const engine = new LayoutEngine({ text: 'abcdefgh', wrapColumn: 4 });
  for (const [row, width] of [[0, 4], [1, 4]] as const) {
    for (let column = 0; column <= width; column++) {
      const hit = engine.visualToLogical({ line: 0, row, column });
      assert.deepEqual(engine.logicalToVisual(hit.offset, hit.affinity), {
        line: 0,
        row,
        column,
      });
    }
  }
});

test('wide char that does not fit wraps as a whole', () => {
  const engine = new LayoutEngine({ text: 'ab世cd', wrapColumn: 3 });
  assert.equal(engine.lineHeight(0), 3);
  const rows = engine.allRows();
  assert.deepEqual(rows.map((r) => [r.startOffset, r.endOffset, r.width]), [
    [0, 2, 2],
    [2, 4, 3],
    [4, 5, 1],
  ]);
  // 世 starts row 1 at column 0.
  assert.deepEqual(engine.logicalToVisual(2), { line: 0, row: 1, column: 0 });
});

test('tab wraps to the next row when its tab stop exceeds the wrap column', () => {
  const engine = new LayoutEngine({ text: 'abcd\te', tabWidth: 4, wrapColumn: 6 });
  // tab at column 4 wants column 8 > 6, so it wraps and expands 0..4 on row 1.
  const rows = engine.allRows();
  assert.deepEqual(rows.map((r) => [r.startOffset, r.endOffset, r.width]), [
    [0, 4, 4],
    [4, 6, 5],
  ]);
  assert.deepEqual(engine.logicalToVisual(5), { line: 0, row: 1, column: 4 }); // 'e'
});

test('LF and CRLF line terminators', () => {
  const lf = new LayoutEngine({ text: 'ab\ncd\n' });
  assert.equal(lf.lineCount, 3);
  assert.deepEqual(lf.logicalToVisual(3), { line: 1, row: 0, column: 0 });
  assert.equal(lf.visualToLogical({ line: 1, row: 0, column: 0 }).offset, 3);

  const crlf = new LayoutEngine({ text: 'ab\r\ncd\r\n' });
  assert.equal(crlf.lineCount, 3);
  // Line 1 starts after the two-code-unit CRLF terminator.
  assert.deepEqual(crlf.logicalToVisual(4), { line: 1, row: 0, column: 0 });
  assert.equal(crlf.visualToLogical({ line: 1, row: 0, column: 0 }).offset, 4);
  assert.equal(crlf.visualToLogical({ line: 0, row: 0, column: 2 }).offset, 2); // end of "ab", before \r
});

test('empty document and empty lines still have one row each', () => {
  const empty = new LayoutEngine({ text: '' });
  assert.equal(empty.lineCount, 1);
  assert.equal(empty.lineHeight(0), 1);
  assert.deepEqual(empty.logicalToVisual(0), { line: 0, row: 0, column: 0 });

  const blank = new LayoutEngine({ text: 'a\n\nb' });
  assert.equal(blank.lineCount, 3);
  assert.equal(blank.lineHeight(1), 1);
  assert.equal(blank.visualToLogical({ line: 1, row: 0, column: 0 }).offset, 2);
});
