/** Glyph metrics are fixture-given; no browser layout engine is involved. */
export interface GlyphMetrics {
  charWidth: Readonly<Record<string, number>>;
  defaultWidth: number;
  padding: number;
  minColumnWidth: number;
}

export function measureText(text: string, metrics: GlyphMetrics): number {
  let width = 0;
  for (const ch of text) width += metrics.charWidth[ch] ?? metrics.defaultWidth;
  return width;
}

export function columnWidth(title: string, metrics: GlyphMetrics): number {
  return Math.max(metrics.minColumnWidth, measureText(title, metrics) + metrics.padding);
}
