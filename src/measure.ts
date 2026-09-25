// Glyph metrics come from a fixture table. Core math never touches a
// browser layout engine; widths are pure functions of the label string.

export interface GlyphMetrics {
  charWidths: Record<string, number>;
  defaultWidth: number;
  padding: number;
}

export function measureLabel(label: string, metrics: GlyphMetrics): number {
  let width = metrics.padding;
  for (const ch of label) {
    width += metrics.charWidths[ch] ?? metrics.defaultWidth;
  }
  return width;
}
