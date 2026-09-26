// Pure layout core: greedy line breaking + cursor geometry.
// Deterministic, no DOM, no browser layout engine.

import type { GlyphMetrics, NodeTree } from './fixtures.ts';

export interface LineBox {
  readonly nodeId: string;
  readonly index: number;      // line index within the paragraph
  readonly start: number;      // char offset of line start within paragraph text
  readonly end: number;        // char offset (exclusive) of line end
  readonly width: number;      // px
  readonly y: number;          // px from document top
}

export interface DocumentLayout {
  readonly lines: readonly LineBox[];
  readonly height: number;
}

export function glyphAdvance(metrics: GlyphMetrics, ch: string): number {
  if (ch === ' ') return metrics.spaceAdvance;
  return metrics.advance[ch] ?? metrics.fallbackAdvance;
}

export function textWidth(metrics: GlyphMetrics, text: string): number {
  let w = 0;
  for (const ch of text) w += glyphAdvance(metrics, ch);
  return w;
}

/** Greedy word wrap for a single paragraph. Breaks at spaces; hard-breaks long words. */
export function layoutParagraph(
  metrics: GlyphMetrics,
  nodeId: string,
  text: string,
  maxWidth: number,
  yOffset: number,
): LineBox[] {
  const lines: LineBox[] = [];
  const push = (start: number, end: number) => {
    lines.push({
      nodeId,
      index: lines.length,
      start,
      end,
      width: textWidth(metrics, text.slice(start, end)),
      y: yOffset + lines.length * metrics.lineHeight,
    });
  };

  if (text.length === 0) {
    push(0, 0);
    return lines;
  }

  let lineStart = 0;
  let width = 0;
  let lastSpace = -1; // index of last breakable space
  let i = 0;
  while (i < text.length) {
    const ch = text[i];
    const adv = glyphAdvance(metrics, ch);
    if (ch === ' ') lastSpace = i;
    if (width + adv > maxWidth && i > lineStart) {
      if (lastSpace >= lineStart) {
        push(lineStart, lastSpace);
        lineStart = lastSpace + 1;
        i = lineStart;
      } else {
        push(lineStart, i);
        lineStart = i;
      }
      width = 0;
      lastSpace = -1;
      continue;
    }
    width += adv;
    i += 1;
  }
  push(lineStart, text.length);
  return lines;
}

/** Layout the whole document tree (paragraphs stacked vertically). */
export function layoutDocument(
  metrics: GlyphMetrics,
  tree: NodeTree,
  maxWidth: number,
): DocumentLayout {
  const lines: LineBox[] = [];
  let y = 0;
  const walk = (node: NodeTree) => {
    if (node.kind === 'paragraph') {
      const paraLines = layoutParagraph(metrics, node.id, node.text ?? '', maxWidth, y);
      lines.push(...paraLines);
      y += paraLines.length * metrics.lineHeight;
    }
    for (const child of node.children ?? []) walk(child);
  };
  walk(tree);
  return { lines, height: y };
}

/** Cursor (x, y) for a char offset inside a paragraph, from the computed layout. */
export function cursorRect(
  metrics: GlyphMetrics,
  layout: DocumentLayout,
  nodeId: string,
  text: string,
  offset: number,
): { x: number; y: number; lineIndex: number } {
  const paraLines = layout.lines.filter((l) => l.nodeId === nodeId);
  const line = paraLines.find((l) => offset >= l.start && offset <= l.end) ?? paraLines[paraLines.length - 1];
  const x = textWidth(metrics, text.slice(line.start, Math.min(offset, line.end)));
  return { x, y: line.y, lineIndex: line.index };
}
