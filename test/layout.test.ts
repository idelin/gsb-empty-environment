import { test } from 'node:test';
import assert from 'node:assert/strict';
import { layoutLine } from '../src/index.ts';
import { mixedRuns, rtlOnlyRuns } from './fixtures.ts';

test('mixed-direction line: per-glyph position table', () => {
  const line = layoutLine(mixedRuns());
  assert.equal(line.x, 0);
  assert.equal(line.width, 62);

  const table = line.positions.map((p) => ({
    run: p.runIndex,
    glyph: p.glyphIndex,
    dir: p.direction,
    start: p.start,
    end: p.end,
    x: p.x,
    width: p.width,
  }));
  assert.deepEqual(table, [
    { run: 0, glyph: 0, dir: 'ltr', start: 0, end: 1, x: 0, width: 10 },
    { run: 0, glyph: 1, dir: 'ltr', start: 1, end: 2, x: 10, width: 10 },
    { run: 0, glyph: 2, dir: 'ltr', start: 2, end: 4, x: 20, width: 14 },
    { run: 1, glyph: 0, dir: 'rtl', start: 4, end: 5, x: 42, width: 8 },
    { run: 1, glyph: 1, dir: 'rtl', start: 5, end: 6, x: 42, width: 0 },
    { run: 1, glyph: 2, dir: 'rtl', start: 6, end: 7, x: 34, width: 8 },
    { run: 2, glyph: 0, dir: 'ltr', start: 7, end: 8, x: 50, width: 12 },
  ]);

  const runBounds = line.runs.map((r) => ({
    x: r.x,
    width: r.width,
    logicalStart: r.logicalStart,
    logicalEnd: r.logicalEnd,
  }));
  assert.deepEqual(runBounds, [
    { x: 0, width: 34, logicalStart: 0, logicalEnd: 4 },
    { x: 34, width: 16, logicalStart: 4, logicalEnd: 7 },
    { x: 50, width: 12, logicalStart: 7, logicalEnd: 8 },
  ]);
});

test('rtl-only line: first logical glyph is rightmost', () => {
  const line = layoutLine(rtlOnlyRuns());
  assert.equal(line.width, 18);
  const table = line.positions.map((p) => ({ start: p.start, x: p.x }));
  assert.deepEqual(table, [
    { start: 0, x: 12 },
    { start: 1, x: 6 },
    { start: 2, x: 0 },
  ]);
});

test('non-zero origin shifts every glyph', () => {
  const line = layoutLine(mixedRuns(), 100);
  assert.equal(line.x, 100);
  assert.equal(line.width, 62);
  assert.equal(line.positions[0]!.x, 100);
  assert.equal(line.positions[3]!.x, 142);
});

test('empty line has no positions and zero width', () => {
  const line = layoutLine([]);
  assert.equal(line.width, 0);
  assert.deepEqual(line.positions, []);
  assert.deepEqual(line.runs, []);
});
