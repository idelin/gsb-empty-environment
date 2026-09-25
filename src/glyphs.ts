import type { GlyphMetrics } from './types.ts';

/** Text width from fixture metrics only — no browser measurement. */
export function measureText(text: string, metrics: GlyphMetrics): number {
  let width = 0;
  for (const ch of text) {
    width += metrics.advances[ch] ?? metrics.defaultAdvance;
  }
  return width;
}

/** Content width plus horizontal padding, i.e. the width a cell needs. */
export function measureCellContent(text: string, metrics: GlyphMetrics): number {
  return measureText(text, metrics) + 2 * metrics.cellPaddingX;
}
