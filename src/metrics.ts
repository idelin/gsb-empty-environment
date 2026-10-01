/** 字形度量：由夹具提供，用于在测试内自算文本宽度（不借浏览器布局引擎）。 */

export interface GlyphMetrics {
  /** 每个字符的前进宽度（px）。 */
  advance: Record<string, number>;
  /** 未收录字符的回退宽度。 */
  fallback: number;
}

export function measureText(text: string, metrics: GlyphMetrics): number {
  let width = 0;
  for (const ch of text) {
    width += metrics.advance[ch] ?? metrics.fallback;
  }
  return width;
}
