// Versioned glyph metrics. Widths come from fixture tables only; no
// browser layout engine is involved.

import type { ClusterItem, MetricTable } from './types.ts';

export function clusterWidth(text: string, table: MetricTable): number {
  const exact = table.clusters[text];
  if (exact !== undefined) return exact;
  if (/^\s+$/.test(text)) return table.spaceWidth ?? table.defaultWidth;
  return table.defaultWidth;
}

export function ellipsisWidth(table: MetricTable): number {
  return table.ellipsisWidth ?? clusterWidth('…', table);
}

export function measure(items: ClusterItem[], table: MetricTable, ellipsis: string): void {
  for (const it of items) {
    it.width = it.kind === 'ellipsis' ? ellipsisWidth(table) : clusterWidth(it.text, table);
  }
}

export const ELLIPSIS = '…';

export function makeEllipsisItem(dir: ClusterItem['dir']): ClusterItem {
  return {
    kind: 'ellipsis',
    text: ELLIPSIS,
    srcStart: -1,
    srcEnd: -1,
    dir,
    key: false,
    required: false,
    atomicId: 0,
    breakAfter: false,
    width: 0,
  };
}
