import { test } from 'node:test';
import assert from 'node:assert/strict';
import { extractRowRuns, runsToRow } from '../src/rle.ts';

test('all-white row is a single run', () => {
  assert.deepEqual(extractRowRuns(new Uint8Array(100)), [100]);
});

test('all-black row starts with a zero-length white run', () => {
  const row = new Uint8Array(50).fill(1);
  assert.deepEqual(extractRowRuns(row), [0, 50]);
});

test('alternating pixels produce unit runs', () => {
  const row = new Uint8Array([1, 0, 1, 0, 1]);
  assert.deepEqual(extractRowRuns(row), [0, 1, 1, 1, 1, 1]);
});

test('runsToRow is the exact inverse of extractRowRuns', () => {
  const rng = (() => {
    let s = 42;
    return () => {
      s = (s * 1103515245 + 12345) & 0x7fffffff;
      return s / 0x7fffffff;
    };
  })();
  for (const width of [1, 7, 64, 255, 1000]) {
    const row = new Uint8Array(width);
    for (let i = 0; i < width; i++) row[i] = rng() < 0.1 ? 1 : 0;
    const runs = extractRowRuns(row);
    assert.equal(runs.reduce((a, b) => a + b, 0), width);
    assert.deepEqual([...runsToRow(runs, width)], [...row]);
  }
});

test('runsToRow rejects runs that do not fill the row', () => {
  assert.throws(() => runsToRow([10, 5], 100), /sum to 15/);
});
