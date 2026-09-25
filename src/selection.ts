import type { HeaderSnapshot } from './types.ts';

/** Inclusive data-index range within one snapshot version. */
export interface SelectionRegion {
  start: number;
  end: number;
}

export type MappingResult =
  | { status: 'mapped'; candidates: SelectionRegion[] }
  | { status: 'invalidated'; missingKeys: string[] };

/**
 * Map a selection region from an old snapshot onto a newer one.
 * - Every selected column key must still exist, otherwise the mapping is
 *   invalidated and the missing keys are reported.
 * - Surviving keys are grouped into contiguous runs; a reorder can split one
 *   old region into several candidate regions.
 */
export function mapSelection(
  from: HeaderSnapshot,
  to: HeaderSnapshot,
  region: SelectionRegion,
): MappingResult {
  const keys: string[] = [];
  for (let i = region.start; i <= region.end; i++) {
    const column = from.columns[i];
    if (column === undefined) throw new RangeError(`selection index ${i} out of range`);
    keys.push(column.key);
  }
  const missingKeys = keys.filter((key) => !(key in to.dataIndexByKey));
  if (missingKeys.length > 0) return { status: 'invalidated', missingKeys };

  const indices = keys.map((key) => to.dataIndexByKey[key]).sort((a, b) => a - b);
  const candidates: SelectionRegion[] = [];
  let start = indices[0];
  let prev = indices[0];
  for (let i = 1; i < indices.length; i++) {
    if (indices[i] === prev + 1) {
      prev = indices[i];
      continue;
    }
    candidates.push({ start, end: prev });
    start = indices[i];
    prev = indices[i];
  }
  candidates.push({ start, end: prev });
  return { status: 'mapped', candidates };
}
