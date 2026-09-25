import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildGrid } from '../src/grid.ts';
import { measureText } from '../src/glyphs.ts';
import { loadGlyphFixture, loadTableFixture } from '../src/fixtures.ts';
import type { GlyphMetrics } from '../src/types.ts';

const metrics = loadGlyphFixture();
const grid = buildGrid(loadTableFixture(), metrics);

test('glyph measurement uses fixture advances with default fallback', () => {
  // 'A' falls back to defaultAdvance (5), 'l' is 2, others 5.
  assert.equal(measureText('Alpha', metrics), 5 + 2 + 5 + 5 + 5);
  assert.equal(measureText('Gamma', metrics), 5 + 5 + 8 + 8 + 5);
});

test('placement follows HTML table auto-placement with spans', () => {
  assert.equal(grid.rowCount, 4);
  assert.equal(grid.colCount, 4);
  const anchors = grid.cells.map((c) => `${c.text}@${c.anchor.row},${c.anchor.col}`);
  assert.deepEqual(anchors, [
    'Alpha@0,0', 'Beta@0,2', 'Gamma@0,3',
    'Delta@1,0', 'Epsilon@1,1', 'Zeta@1,2',
    'Eta@2,0', 'Theta@2,2', 'Iota@2,3',
    'Kappa@3,2',
  ]);
});

test('column widths and row heights derive from glyph metrics', () => {
  assert.deepEqual(grid.columnWidths, [25, 33, 28, 35]);
  assert.deepEqual(grid.rowHeights, [14, 14, 14, 14]);
});

test('merged cell pixel rects union their spanned slots', () => {
  const byText = new Map(grid.cells.map((c) => [c.text, c]));
  assert.deepEqual(
    ['x', 'y', 'width', 'height'].map((k) => byText.get('Alpha')![k as 'x']),
    [0, 0, 58, 14],
  );
  assert.deepEqual(
    ['x', 'y', 'width', 'height'].map((k) => byText.get('Gamma')![k as 'x']),
    [86, 0, 35, 28],
  );
  assert.deepEqual(
    ['x', 'y', 'width', 'height'].map((k) => byText.get('Eta')![k as 'x']),
    [0, 28, 58, 28],
  );
  assert.deepEqual(
    ['x', 'y', 'width', 'height'].map((k) => byText.get('Kappa')![k as 'x']),
    [58, 42, 63, 14],
  );
});

test('spanned cell wider than its columns widens the last spanned column', () => {
  const wide = buildGrid(
    {
      rows: [
        { cells: [{ text: 'WWWWWWWW', colSpan: 2 }] },
        { cells: [{ text: 'a' }, { text: 'b' }] },
      ],
    },
    metrics,
  );
  // Each single-char column needs 5 + 4 = 9; the header needs 8*8 + 4 = 68.
  // Deficit 68 - 18 = 50 lands on the last spanned column.
  assert.deepEqual(wide.columnWidths, [9, 59]);
});

test('covered slots are reported, anchors are not', () => {
  assert.equal(grid.isCovered(0, 1), true); // inside Alpha
  assert.equal(grid.isCovered(1, 3), true); // inside Gamma
  assert.equal(grid.isCovered(3, 0), true); // inside Eta
  assert.equal(grid.isCovered(0, 0), false); // Alpha anchor
  assert.equal(grid.isCovered(1, 1), false); // plain cell
  assert.equal(grid.cellAt(0, 1), null);
  assert.equal(grid.cellAt(0, 0)?.text, 'Alpha');
});
