import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildGrid } from '../src/grid.ts';
import { loadGlyphFixture, loadTableFixture } from '../src/fixtures.ts';
import { selectRange } from '../src/selection.ts';
import { copyRange } from '../src/clipboard.ts';

const grid = buildGrid(loadTableFixture(), loadGlyphFixture());

function copy(anchorRow: number, anchorCol: number, focusRow: number, focusCol: number): string {
  const rect = selectRange(
    grid,
    { anchor: { row: anchorRow, col: anchorCol }, focus: { row: focusRow, col: focusCol } },
  );
  return copyRange(grid, rect);
}

test('copying a covered slot yields its anchor text plus an empty placeholder', () => {
  // (0,1) is covered by Alpha; selection expands to the whole 1x2 region.
  assert.equal(copy(0, 1, 0, 1), 'Alpha\t');
});

test('copy preserves rectangular structure with empty placeholders', () => {
  // Chain expansion over Eta -> Kappa -> Gamma selects the whole grid.
  const expected = [
    'Alpha\t\tBeta\tGamma',
    'Delta\tEpsilon\tZeta\t',
    'Eta\t\tTheta\tIota',
    '\t\tKappa\t',
  ].join('\n');
  assert.equal(copy(1, 1, 2, 2), expected);
});

test('copy of a merge-free range is a plain TSV block', () => {
  assert.equal(copy(2, 2, 2, 3), 'Theta\tIota');
});

test('copy range grows with the selection before placeholders are emitted', () => {
  // (1,0)-(2,1) clips Eta, so the copy covers rows 1-3 including Eta's
  // covered slots as empty fields.
  assert.equal(copy(1, 0, 2, 1), ['Delta\tEpsilon', 'Eta\t', '\t'].join('\n'));
});

test('row count and column count of the copy match the rectangle', () => {
  const text = copy(0, 0, 3, 3);
  const rows = text.split('\n');
  assert.equal(rows.length, 4);
  for (const row of rows) {
    assert.equal(row.split('\t').length, 4);
  }
});
