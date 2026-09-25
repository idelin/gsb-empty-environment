import { buildGrid } from './grid.ts';
import { loadGlyphFixture, loadTableFixture } from './fixtures.ts';
import { move, type Direction } from './navigation.ts';
import { normalizeRect, selectRange } from './selection.ts';
import { copyRange } from './clipboard.ts';
import type { Coord } from './types.ts';

const grid = buildGrid(loadTableFixture(), loadGlyphFixture());

const fmt = (c: Coord) => `(${c.row},${c.col})`;

console.log('=== Layout (computed from fixtures, no browser) ===');
console.log(`grid: ${grid.rowCount} rows x ${grid.colCount} cols`);
console.log(`column widths: [${grid.columnWidths.join(', ')}]`);
console.log(`row heights:   [${grid.rowHeights.join(', ')}]`);
for (const cell of grid.cells) {
  console.log(
    `  ${cell.text.padEnd(8)} anchor=${fmt(cell.anchor)} span=${cell.rowSpan}x${cell.colSpan}` +
      ` rect=(x:${cell.x}, y:${cell.y}, w:${cell.width}, h:${cell.height})`,
  );
}

console.log('\n=== 2D navigation (jumps never land on covered slots) ===');
let cursor: Coord = { row: 1, col: 0 }; // Delta
console.log(`start at ${fmt(cursor)} (${grid.cellAt(cursor.row, cursor.col)?.text})`);
const walk: Direction[] = ['right', 'right', 'right', 'up', 'down', 'down', 'left', 'down'];
for (const dir of walk) {
  const next = move(grid, cursor, dir);
  const cell = grid.cellAt(next.row, next.col);
  const note = next === cursor || (next.row === cursor.row && next.col === cursor.col) ? ' (edge: stay)' : '';
  console.log(`  ${dir.padEnd(5)} -> ${fmt(next)} (${cell?.text})${note}`);
  cursor = next;
}

console.log('\n=== Range selection expands to a fixed point over merges ===');
const anchor = { row: 1, col: 1 };
const focus = { row: 2, col: 2 };
const raw = normalizeRect(anchor, focus);
console.log(
  `raw rect:      rows ${raw.top}..${raw.bottom}, cols ${raw.left}..${raw.right}`,
);
const expanded = selectRange(grid, { anchor, focus });
console.log(
  `fixed point:   rows ${expanded.top}..${expanded.bottom}, cols ${expanded.left}..${expanded.right}` +
    '  (chain: Eta -> Kappa -> Gamma)',
);

console.log('\n=== Copy (rectangular, empty placeholders for covered slots) ===');
console.log(copyRange(grid, expanded).replace(/^/gm, '  | '));
