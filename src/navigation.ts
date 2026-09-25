import type { Grid } from './grid.ts';
import type { Coord } from './types.ts';

export type Direction = 'up' | 'down' | 'left' | 'right';

const DELTAS: Record<Direction, Coord> = {
  up: { row: -1, col: 0 },
  down: { row: 1, col: 0 },
  left: { row: 0, col: -1 },
  right: { row: 0, col: 1 },
};

/**
 * Move one logical step from an anchor cell. The landing slot is always
 * resolved to the anchor of the region covering it, so a jump never lands
 * on a covered (non-anchor) slot. Out-of-bounds moves stay in place.
 */
export function move(grid: Grid, from: Coord, direction: Direction): Coord {
  const cell = grid.cellAt(from.row, from.col);
  if (!cell) return from; // `from` must be an anchor; otherwise refuse to move

  // First step clears the current cell's own merged region: one slot past
  // its near edge (up/left) or far edge (down/right).
  let row = from.row;
  let col = from.col;
  if (direction === 'up') row -= 1;
  else if (direction === 'down') row += cell.rowSpan;
  else if (direction === 'left') col -= 1;
  else col += cell.colSpan;

  const delta = DELTAS[direction];
  while (grid.contains(row, col)) {
    const anchor = grid.anchorAt(row, col);
    if (!anchor) return from; // hole in the grid: treat as blocked
    if (anchor.row === from.row && anchor.col === from.col) {
      // Still inside the starting region; keep stepping in the same axis.
      row += delta.row;
      col += delta.col;
      continue;
    }
    return anchor;
  }
  return from; // grid edge: stay
}
