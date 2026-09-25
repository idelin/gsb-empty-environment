/**
 * Fixture-driven layout engine.
 *
 * The node tree and the glyph metrics are supplied as data (fixtures); every
 * computation — text measurement, line wrapping, box stacking — is pure
 * arithmetic performed here. No browser layout engine is involved.
 */

export interface GlyphMetrics {
  /** Advance width per character, in layout units. */
  advances: Record<string, number>;
  /** Fallback advance for characters missing from `advances`. */
  defaultAdvance: number;
  /** Line height in layout units. */
  lineHeight: number;
}

export function measureText(text: string, m: GlyphMetrics): number {
  let w = 0;
  for (const ch of text) {
    w += m.advances[ch] ?? m.defaultAdvance;
  }
  return w;
}

/**
 * Greedy word wrap: pack space-separated words onto lines of at most
 * `maxWidth`. A single word wider than `maxWidth` is hard-broken by
 * characters. Deterministic and purely metric-driven.
 */
export function wrapText(text: string, maxWidth: number, m: GlyphMetrics): string[] {
  const space = m.advances[' '] ?? m.defaultAdvance;
  const lines: string[] = [];
  let current = '';
  const pushWord = (word: string) => {
    const wordW = measureText(word, m);
    if (current === '') {
      if (wordW <= maxWidth) {
        current = word;
        return;
      }
      // Hard-break an overlong word by characters.
      let chunk = '';
      let chunkW = 0;
      for (const ch of word) {
        const cw = m.advances[ch] ?? m.defaultAdvance;
        if (chunkW + cw > maxWidth && chunk !== '') {
          lines.push(chunk);
          chunk = '';
          chunkW = 0;
        }
        chunk += ch;
        chunkW += cw;
      }
      current = chunk;
      return;
    }
    const joined = measureText(current, m) + space + wordW;
    if (joined <= maxWidth) {
      current = current + ' ' + word;
    } else {
      lines.push(current);
      current = '';
      pushWord(word);
    }
  };
  for (const word of text.split(' ')) {
    if (word === '') continue;
    pushWord(word);
  }
  if (current !== '') lines.push(current);
  return lines;
}

export type NodeSpec =
  | {
      type: 'box';
      id?: string;
      padding?: number;
      gap?: number;
      children: NodeSpec[];
    }
  | {
      type: 'text';
      id?: string;
      text: string;
    };

export interface TextLine {
  text: string;
  y: number;
  width: number;
}

export interface LayoutBox {
  id?: string;
  x: number;
  y: number;
  width: number;
  height: number;
  lines?: TextLine[];
  children?: LayoutBox[];
}

/**
 * Lay out a node tree at a fixed container width. Boxes stack their children
 * vertically (block flow) with padding and gap; text nodes wrap into lines.
 * Coordinates are absolute with the root at (0, 0).
 */
export function layout(
  node: NodeSpec,
  width: number,
  m: GlyphMetrics,
  x = 0,
  y = 0,
): LayoutBox {
  if (node.type === 'text') {
    const wrapped = wrapText(node.text, width, m);
    const lines: TextLine[] = wrapped.map((text, i) => ({
      text,
      y: y + i * m.lineHeight,
      width: measureText(text, m),
    }));
    const box: LayoutBox = {
      x,
      y,
      width,
      height: wrapped.length * m.lineHeight,
      lines,
    };
    if (node.id !== undefined) box.id = node.id;
    return box;
  }
  const padding = node.padding ?? 0;
  const gap = node.gap ?? 0;
  const contentWidth = width - 2 * padding;
  const children: LayoutBox[] = [];
  let childY = y + padding;
  for (const child of node.children) {
    const childBox = layout(child, contentWidth, m, x + padding, childY);
    children.push(childBox);
    childY += childBox.height + gap;
  }
  const contentHeight =
    children.length === 0
      ? 0
      : children.reduce((sum, c) => sum + c.height, 0) + gap * (children.length - 1);
  const box: LayoutBox = {
    x,
    y,
    width,
    height: contentHeight + 2 * padding,
    children,
  };
  if (node.id !== undefined) box.id = node.id;
  return box;
}
