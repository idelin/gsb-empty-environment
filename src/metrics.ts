/**
 * Pure text measurement against a fixture-provided glyph metric table.
 * No browser layout engine is involved: widths are summed from per-glyph
 * advance widths supplied by the fixture.
 */
export interface GlyphMetrics {
  unitsPerEm: number;
  /** Advance width (in font units) per glyph. */
  advances: Readonly<Record<string, number>>;
  /** Fallback advance for glyphs missing from `advances`. */
  defaultAdvance: number;
}

export function measureWidth(text: string, metrics: GlyphMetrics): number {
  let width = 0;
  for (const ch of text) {
    width += metrics.advances[ch] ?? metrics.defaultAdvance;
  }
  return width;
}

/**
 * Truncate `text` so its measured width (including the ellipsis when
 * truncation happens) fits within `maxWidth`.
 */
export function truncateToWidth(
  text: string,
  metrics: GlyphMetrics,
  maxWidth: number,
  ellipsis = "…",
): string {
  if (measureWidth(text, metrics) <= maxWidth) return text;
  const ellipsisWidth = measureWidth(ellipsis, metrics);
  if (ellipsisWidth > maxWidth) return "";
  let out = "";
  let width = 0;
  for (const ch of text) {
    const advance = metrics.advances[ch] ?? metrics.defaultAdvance;
    if (width + advance + ellipsisWidth > maxWidth) break;
    width += advance;
    out += ch;
  }
  return out + ellipsis;
}
