import type { GlyphMetrics } from './types.ts';

/**
 * Text measurement from fixture-provided glyph metrics. The core never
 * touches a browser layout engine; widths are pure functions of the metrics.
 */
export function measureText(text: string, metrics: GlyphMetrics): number {
  let w = 0;
  for (const ch of text) w += metrics.advance[ch] ?? metrics.defaultAdvance;
  return w;
}

export interface RowGeometry {
  id: string;
  depth: number;
  x: number;
  width: number;
}

export function rowGeometry(
  id: string,
  depth: number,
  label: string,
  metrics: GlyphMetrics,
  indentPx: number,
): RowGeometry {
  const x = depth * indentPx;
  return { id, depth, x, width: x + measureText(label, metrics) };
}

/** Width of the widest visible row; drives horizontal scroll extents. */
export function contentWidth(rows: RowGeometry[]): number {
  let max = 0;
  for (const r of rows) max = Math.max(max, r.width);
  return max;
}
