import type { RowId } from '../src/types.ts';

/**
 * Independent full-layout reference. Recomputes everything from scratch
 * with plain loops (no prefix-index tricks), so the differential test can
 * catch incremental-index bugs by disagreement.
 */

export interface RefRow {
  id: RowId;
  groupId: string | null;
}

export interface RefLayout {
  visibleIds: RowId[];
  heights: number[];
  prefix: number[];
  total: number;
}

export function computeLayout(
  rows: readonly RefRow[],
  collapsed: ReadonlySet<string>,
  measured: ReadonlyMap<RowId, number>,
  estimated: number,
): RefLayout {
  const visibleIds: RowId[] = [];
  const heights: number[] = [];
  for (const row of rows) {
    if (row.groupId !== null && collapsed.has(row.groupId)) continue;
    visibleIds.push(row.id);
    heights.push(measured.get(row.id) ?? estimated);
  }
  const prefix = [0];
  for (const h of heights) prefix.push(prefix[prefix.length - 1] + h);
  return { visibleIds, heights, prefix, total: prefix[prefix.length - 1] };
}

export function refIndexAtOffset(layout: RefLayout, y: number): number {
  if (layout.visibleIds.length === 0) return -1;
  let i = 0;
  for (let k = 1; k < layout.prefix.length; k++) {
    if (layout.prefix[k] <= y) i = k;
    else break;
  }
  return Math.min(i, layout.visibleIds.length - 1);
}

export function refVisibleIds(layout: RefLayout, scrollTop: number, viewport: number): RowId[] {
  const start = refIndexAtOffset(layout, scrollTop);
  if (start === -1) return [];
  const limit = scrollTop + viewport;
  const ids: RowId[] = [];
  for (let i = start; i < layout.visibleIds.length && layout.prefix[i] < limit; i++) {
    ids.push(layout.visibleIds[i]);
  }
  return ids;
}

export interface RefAnchor {
  id: RowId;
  offset: number;
  index: number;
}

export function refCapture(layout: RefLayout, scrollTop: number): RefAnchor | null {
  const i = refIndexAtOffset(layout, scrollTop);
  if (i === -1) return null;
  return { id: layout.visibleIds[i], offset: scrollTop - layout.prefix[i], index: i };
}

/** Reference implementation of the public fallback rules. */
export function refRestore(
  saved: RefAnchor | null,
  before: RefLayout,
  after: RefLayout,
  scrollTopBefore: number,
  viewport: number,
): number {
  const maxScroll = Math.max(0, after.total - viewport);
  const clamp = (y: number): number => Math.min(Math.max(y, 0), maxScroll);
  if (!saved) return clamp(scrollTopBefore);
  let i = after.visibleIds.indexOf(saved.id);
  if (i === -1) {
    const survivors = new Set(after.visibleIds);
    let fallback: RowId | null = null;
    for (let k = saved.index + 1; k < before.visibleIds.length; k++) {
      if (survivors.has(before.visibleIds[k])) {
        fallback = before.visibleIds[k];
        break;
      }
    }
    if (fallback === null) {
      for (let k = saved.index - 1; k >= 0; k--) {
        if (survivors.has(before.visibleIds[k])) {
          fallback = before.visibleIds[k];
          break;
        }
      }
    }
    if (fallback !== null) i = after.visibleIds.indexOf(fallback);
  }
  if (i === -1) i = Math.min(saved.index, after.visibleIds.length - 1);
  if (i < 0) return 0;
  const offset = Math.min(saved.offset, after.heights[i]);
  return clamp(after.prefix[i] + offset);
}
