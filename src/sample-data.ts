/**
 * Deterministic 100-column sample table:
 *  - column "a": numeric filter column
 *  - column "b": numeric sort column
 *  - columns "c0".."c97": wide expensive string columns (the 98 columns
 *    that must NOT be read until the final top-k rows are known)
 */

import { ColumnStore } from './column-store.ts';

export const NUM_COLUMNS = 100;
export const EXPENSIVE_COLUMN_PREFIX = 'c';

export function buildSampleTable(rowCount: number): ColumnStore {
  const store = new ColumnStore(rowCount);

  // Deterministic pseudo-random generator (mulberry32) so tests are stable.
  let seed = 0x2f6e2b1;
  const rand = (): number => {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };

  const a: number[] = [];
  const b: number[] = [];
  for (let i = 0; i < rowCount; i++) {
    a.push(Math.floor(rand() * 100)); // 0..99  => ~90% pass "a > 10"
    b.push(Math.floor(rand() * 1_000_000)); // wide sort domain
  }
  store.addColumn('a', a);
  store.addColumn('b', b);

  for (let c = 0; c < NUM_COLUMNS - 2; c++) {
    const name = `${EXPENSIVE_COLUMN_PREFIX}${c}`;
    const col: string[] = new Array(rowCount);
    for (let i = 0; i < rowCount; i++) {
      col[i] = `${name}-row${i}-` + 'x'.repeat(64); // bulky string payload
    }
    store.addColumn(name, col);
  }
  return store;
}
