import type { ContentNode } from './types.ts';

/**
 * Glyph metrics supplied by fixtures. `advance(ch)` returns the horizontal
 * advance of a single glyph in pixels. This is the only input the text
 * measurer needs; layout is simulated, never delegated to a browser.
 */
export interface GlyphMetrics {
  lineHeight: number;
  advance(ch: string): number;
}

export function glyphMetricsFromFixture(
  table: Record<string, number>,
  lineHeight: number,
  fallbackAdvance: number,
): GlyphMetrics {
  return {
    lineHeight,
    advance(ch: string): number {
      const w = table[ch];
      return w === undefined ? fallbackAdvance : w;
    },
  };
}

/** Greedy word wrap; returns the number of lines for `text` in `width` px. */
export function wrapLineCount(text: string, width: number, metrics: GlyphMetrics): number {
  if (width <= 0) return 1;
  let lines = 1;
  let pen = 0;
  const words = text.split(' ');
  for (const word of words) {
    const wordWidth = [...word].reduce((acc, ch) => acc + metrics.advance(ch), 0);
    const space = metrics.advance(' ');
    if (pen > 0 && pen + space + wordWidth > width) {
      lines += 1;
      pen = 0;
    } else if (pen > 0) {
      pen += space;
    }
    if (wordWidth <= width) {
      pen += wordWidth;
    } else {
      // Word longer than a full line: break it by glyphs.
      for (const ch of word) {
        const cw = metrics.advance(ch);
        if (pen + cw > width) {
          lines += 1;
          pen = 0;
        }
        pen += cw;
      }
    }
  }
  return lines;
}

/** Deterministic height of a content tree at a given container width. */
export function measureNode(node: ContentNode, width: number, metrics: GlyphMetrics): number {
  switch (node.kind) {
    case 'text':
      return wrapLineCount(node.text, width, metrics) * metrics.lineHeight;
    case 'image':
      return node.height;
    case 'spacer':
      return node.height;
    case 'stack': {
      let total = 0;
      node.children.forEach((child, i) => {
        total += measureNode(child, width, metrics);
        if (i < node.children.length - 1) total += node.gap;
      });
      return total;
    }
  }
}
