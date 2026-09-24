import { test } from 'node:test';
import assert from 'node:assert/strict';
import { GraphemeMeasurer, LayoutEngine } from '../src/index.ts';
import type { ClusterInfo, Measurer } from '../src/index.ts';

/** Measurer wrapper that records every line it is asked to measure. */
class CountingMeasurer implements Measurer {
  calls: string[] = [];
  private readonly inner: Measurer;

  constructor(inner: Measurer) {
    this.inner = inner;
  }

  measure(line: string): ClusterInfo[] {
    this.calls.push(line);
    return this.inner.measure(line);
  }
}

test('a height change in one line does not re-measure unrelated lines', () => {
  const counter = new CountingMeasurer(new GraphemeMeasurer());
  const engine = new LayoutEngine({
    text: 'one\ntwo\nthree',
    wrapColumn: 4,
    measurer: counter,
  });

  // Initial full render measures every line exactly once.
  assert.deepEqual([0, 1, 2].map((l) => engine.lineHeight(l)), [1, 1, 2]);
  assert.deepEqual(counter.calls, ['one', 'two', 'three']);

  // Edit only line 1 so it grows from 1 to 3 visual rows.
  counter.calls = [];
  engine.insert(7, 'xyxyxy'); // 'two' -> 'twoxyxyxy' (9 cols, wraps at 4)
  assert.equal(engine.lineHeight(1), 3);
  assert.deepEqual(counter.calls, ['twoxyxyxy']);

  // Querying the untouched lines is served from cache: no re-measurement.
  engine.lineHeight(0);
  engine.lineHeight(2);
  engine.allRows();
  assert.deepEqual(counter.calls, ['twoxyxyxy']);
});

test('deleting lines invalidates only the affected range', () => {
  const counter = new CountingMeasurer(new GraphemeMeasurer());
  const engine = new LayoutEngine({
    text: 'aa\nbb\ncc\ndd',
    wrapColumn: 10,
    measurer: counter,
  });
  engine.allRows();
  counter.calls = [];

  engine.delete(3, 3); // remove 'bb\n'
  assert.equal(engine.getText(), 'aa\ncc\ndd');
  assert.equal(engine.lineCount, 3);
  engine.allRows();
  // 'cc' merged with the deletion boundary and is re-measured; the
  // unrelated lines 'aa' and 'dd' are served from cache.
  assert.deepEqual(counter.calls, ['cc']);
  assert.deepEqual(engine.logicalToVisual(3), { line: 1, row: 0, column: 0 });
});

test('old-layout hit testing via snapshot after an edit', () => {
  const engine = new LayoutEngine({ text: 'ab\t世\ncd', tabWidth: 4 });
  const before = engine.snapshot();
  const oldHit = before.visualToLogical({ line: 0, row: 0, column: 3 }, 'start');
  assert.equal(oldHit.offset, 2); // inside the tab -> tab start

  engine.delete(0, 2); // remove 'ab'; the layout shifts left
  assert.equal(engine.getText(), '\t世\ncd');

  // The live engine answers per the NEW layout...
  assert.equal(engine.visualToLogical({ line: 0, row: 0, column: 3 }, 'start').offset, 0);
  // ...while the snapshot still answers per the OLD layout (old-layout hit).
  assert.deepEqual(before.visualToLogical({ line: 0, row: 0, column: 3 }, 'start'), oldHit);
  assert.equal(before.text, 'ab\t世\ncd');
  assert.deepEqual(before.logicalToVisual(3), { line: 0, row: 0, column: 4 }); // 世 in old layout
});

test('snapshot stays immutable across multiple edits', () => {
  const engine = new LayoutEngine({ text: 'x'.repeat(20), wrapColumn: 5 });
  const snap = engine.snapshot();
  assert.equal(snap.lineHeight(0), 4);
  engine.delete(0, 10);
  engine.insert(0, 'yy');
  assert.equal(engine.lineHeight(0), 3); // 12 chars / 5 cols -> 3 rows
  assert.equal(snap.lineHeight(0), 4); // snapshot untouched
  assert.equal(snap.text, 'x'.repeat(20));
});
