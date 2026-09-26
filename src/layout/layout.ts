// Fixture-driven layout: block boxes stacked vertically, text wrapped using
// glyph advance metrics supplied by a fixture file. No browser layout engine
// is involved in the core computation.

import type { DocNode } from '../engine/model.ts';
import type { ComputedValue } from '../engine/computed.ts';

export interface GlyphMetrics {
  unitsPerEm: number;
  defaultAdvance: number;
  spaceAdvance: number;
  glyphs: Record<string, number>;
}

export interface StyleView {
  get(nodeId: number, prop: string): ComputedValue | undefined;
}

export interface Line {
  text: string;
  width: number;
}

export interface Box {
  nodeId: number;
  x: number; // border-box origin
  y: number;
  width: number; // border-box width
  height: number; // margin-box height (includes vertical margins)
  lines: Line[];
  children: Box[];
}

export function measureText(text: string, fontSize: number, metrics: GlyphMetrics): number {
  let units = 0;
  for (const ch of text) {
    if (ch === ' ') units += metrics.spaceAdvance;
    else units += metrics.glyphs[ch] ?? metrics.defaultAdvance;
  }
  return (units * fontSize) / metrics.unitsPerEm;
}

// Greedy word wrapping; words longer than the line are hard-split by glyph.
export function wrapText(text: string, fontSize: number, maxWidth: number, metrics: GlyphMetrics): Line[] {
  const lines: Line[] = [];
  let current = '';
  let currentWidth = 0;
  const spaceWidth = measureText(' ', fontSize, metrics);
  const push = (): void => {
    if (current.length > 0) lines.push({ text: current, width: currentWidth });
    current = '';
    currentWidth = 0;
  };
  for (const word of text.trim().split(/\s+/).filter((w) => w.length > 0)) {
    const wordWidth = measureText(word, fontSize, metrics);
    if (wordWidth > maxWidth) {
      push();
      for (const ch of word) {
        const chWidth = measureText(ch, fontSize, metrics);
        if (currentWidth + chWidth > maxWidth && current.length > 0) push();
        current += ch;
        currentWidth += chWidth;
      }
      continue;
    }
    const added = current.length === 0 ? wordWidth : currentWidth + spaceWidth + wordWidth;
    if (current.length > 0 && added > maxWidth) {
      push();
      current = word;
      currentWidth = wordWidth;
    } else {
      current = current.length === 0 ? word : `${current} ${word}`;
      currentWidth = added;
    }
  }
  push();
  return lines;
}

function numericStyle(styles: StyleView, nodeId: number, prop: string, fallback: number): number {
  const raw = styles.get(nodeId, prop)?.value;
  if (raw === null || raw === undefined) return fallback;
  const n = parseFloat(raw);
  return Number.isFinite(n) ? n : fallback;
}

export function layoutDocument(node: DocNode, styles: StyleView, metrics: GlyphMetrics, viewportWidth: number): Box {
  const layout = (n: DocNode, x: number, y: number, availableWidth: number): Box => {
    const display = styles.get(n.id, 'display')?.value ?? 'block';
    const margin = numericStyle(styles, n.id, 'margin', 0);
    if (display === 'none') {
      return { nodeId: n.id, x, y, width: 0, height: 0, lines: [], children: [] };
    }
    const fontSize = numericStyle(styles, n.id, 'font-size', 16);
    const lineHeight = numericStyle(styles, n.id, 'line-height', 1.2) * fontSize;
    const padding = numericStyle(styles, n.id, 'padding', 0);
    const widthRaw = styles.get(n.id, 'width')?.value ?? 'auto';
    const borderWidth = widthRaw === 'auto' ? Math.max(0, availableWidth - 2 * margin) : parseFloat(widthRaw);
    const innerWidth = Math.max(0, borderWidth - 2 * padding);
    const boxX = x + margin;
    const boxY = y + margin;
    let cursor = boxY + padding;
    let lines: Line[] = [];
    if (n.text !== null && n.text.trim().length > 0) {
      lines = wrapText(n.text, fontSize, innerWidth, metrics);
      cursor += lines.length * lineHeight;
    }
    const children: Box[] = [];
    for (const child of n.children) {
      const childBox = layout(child, boxX + padding, cursor, innerWidth);
      children.push(childBox);
      cursor += childBox.height;
    }
    const contentHeight = cursor - (boxY + padding);
    const height = margin * 2 + padding * 2 + contentHeight;
    return { nodeId: n.id, x: boxX, y: boxY, width: borderWidth, height, lines, children };
  };
  return layout(node, 0, 0, viewportWidth);
}
