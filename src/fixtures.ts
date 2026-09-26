import { glyphMetricsFromFixture, type GlyphMetrics } from './metrics.ts';
import type { ContentNode } from './types.ts';

export const FIXTURE_LINE_HEIGHT = 20;

/**
 * Fixture glyph table: most glyphs advance 8px, a few narrow/wide ones
 * differ, spaces are 4px. Line height is fixed at 20px. These numbers are
 * the entire "font"; measurement is pure arithmetic over them.
 */
export function fixtureMetrics(): GlyphMetrics {
  const table: Record<string, number> = {
    ' ': 4,
    i: 4,
    l: 4,
    t: 6,
    m: 12,
    w: 12,
    '.': 4,
    ',': 4,
  };
  return glyphMetricsFromFixture(table, FIXTURE_LINE_HEIGHT, 8);
}

export function textNode(text: string): ContentNode {
  return { kind: 'text', text };
}

export function imageNode(width: number, height: number): ContentNode {
  return { kind: 'image', width, height };
}

export function spacerNode(height: number): ContentNode {
  return { kind: 'spacer', height };
}

export function stackNode(children: ContentNode[], gap = 4): ContentNode {
  return { kind: 'stack', children, gap };
}
