/**
 * 字形度量：由夹具（fixture）提供，不依赖浏览器布局引擎。
 * 每个字符对应一个前进宽度（advance width），宽度为 0 的字符
 * （如零宽空格 U+200B、软连字符 U+00AD）视为不渲染。
 */
export interface GlyphMetrics {
  advanceOf(ch: string): number;
}

export function createGlyphMetrics(
  table: Record<string, number>,
  fallbackAdvance = 8,
): GlyphMetrics {
  return {
    advanceOf(ch: string): number {
      const found = table[ch];
      return found === undefined ? fallbackAdvance : found;
    },
  };
}

/** 按字形度量过滤文本：丢弃前进宽度为 0 的字符。 */
export function renderText(text: string, metrics: GlyphMetrics): string {
  let out = '';
  for (const ch of text) {
    if (metrics.advanceOf(ch) > 0) out += ch;
  }
  return out;
}

/** 折叠空白并去首尾空格（模拟可访问名规范中的空白规整）。 */
export function normalizeSpace(text: string): string {
  return text.replace(/\s+/g, ' ').trim();
}
