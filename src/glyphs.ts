/** 字形度量:由夹具提供,核心计算不依赖浏览器布局引擎。 */
export interface GlyphMetrics {
  /** 逐字符前进宽度(显式夹具数据)。 */
  advances: Readonly<Record<string, number>>;
  /** 未收录字符的后备宽度。 */
  fallbackAdvance: number;
  /** 行高。 */
  lineHeight: number;
}

/** 纯计算文本宽度:逐字符累加前进宽度。 */
export function measureText(text: string, metrics: GlyphMetrics): number {
  let width = 0;
  for (const ch of text) {
    width += metrics.advances[ch] ?? metrics.fallbackAdvance;
  }
  return width;
}
