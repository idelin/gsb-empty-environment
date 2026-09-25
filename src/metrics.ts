/**
 * Row layout computed purely from fixture-provided glyph metrics.
 * No browser layout engine is involved: row height, character width and
 * indent width all come from the fixture, and every computation here is
 * plain arithmetic over the visible sequence.
 */

import type { TreeModel } from "./model.ts";

export interface GlyphMetrics {
  rowHeight: number;
  charWidth: number;
  indentWidth: number;
  /** extra width of the expand/collapse affordance */
  toggleWidth: number;
}

export interface RowLayout {
  id: string;
  index: number;
  depth: number;
  y: number;
  height: number;
  labelX: number;
  labelWidth: number;
  rowWidth: number;
}

export function layoutRows(
  model: TreeModel,
  metrics: GlyphMetrics,
  start: number,
  count: number,
): RowLayout[] {
  const rows: RowLayout[] = [];
  for (const node of model.visibleSlice(start, count)) {
    const index = model.indexOf(node);
    const depth = model.depth(node);
    const labelX = depth * metrics.indentWidth + metrics.toggleWidth;
    const labelWidth = node.label.length * metrics.charWidth;
    rows.push({
      id: node.id,
      index,
      depth,
      y: index * metrics.rowHeight,
      height: metrics.rowHeight,
      labelX,
      labelWidth,
      rowWidth: labelX + labelWidth,
    });
  }
  return rows;
}

/** Hit-test a y coordinate against the fixture metrics. */
export function rowAtY(metrics: GlyphMetrics, y: number, total: number): number {
  const index = Math.floor(y / metrics.rowHeight);
  if (index < 0 || index >= total) return -1;
  return index;
}
