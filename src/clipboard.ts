import type { Grid } from './grid.ts';
import type { Rect } from './types.ts';

/**
 * Copy a (already fixed-point-expanded) rectangle as TSV. The rectangular
 * structure is preserved: covered slots of merged regions and holes become
 * empty placeholder fields, anchors contribute their text.
 */
export function copyRange(grid: Grid, rect: Rect): string {
  const lines: string[] = [];
  for (let row = rect.top; row <= rect.bottom; row += 1) {
    const fields: string[] = [];
    for (let col = rect.left; col <= rect.right; col += 1) {
      const cell = grid.cellAt(row, col);
      fields.push(cell ? cell.text : '');
    }
    lines.push(fields.join('\t'));
  }
  return lines.join('\n');
}
