import { createGlyphMetrics } from '../src/metrics.ts';
import type { GlyphMetrics } from '../src/metrics.ts';

/**
 * 字形度量夹具：普通字符前进宽度 8（回退值），
 * 零宽空格 U+200B 与软连字符 U+00AD 宽度为 0（不渲染）。
 */
export function fixtureMetrics(): GlyphMetrics {
  return createGlyphMetrics({ '​': 0, '­': 0 }, 8);
}
