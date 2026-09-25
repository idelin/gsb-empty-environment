// A tiny deterministic layout engine. The node tree and glyph metrics come
// from fixtures; all measurement and wrapping is computed here directly, no
// browser layout engine is involved.

export interface GlyphMetrics {
  unitsPerEm: number;
  lineHeight: number;
  defaultAdvance: number;
  spaceAdvance: number;
  glyphs: Record<string, number>;
}

export interface Style {
  width?: number;
  height?: number;
  padding?: number;
  margin?: number;
  fontSize?: number;
}

export interface BoxNode {
  type: 'box';
  id?: string;
  style?: Style;
  children?: LayoutNode[];
}

export interface TextNode {
  type: 'text';
  id?: string;
  text: string;
  style?: Style;
}

export type LayoutNode = BoxNode | TextNode;

export interface LayoutLine {
  text: string;
  width: number;
  y: number;
}

export interface LayoutBox {
  id?: string;
  x: number;
  y: number;
  width: number;
  height: number;
  lines?: LayoutLine[];
  children?: LayoutBox[];
}

export function measureText(text: string, fontSize: number, metrics: GlyphMetrics): number {
  let width = 0;
  for (const ch of text) {
    const advance = ch === ' ' ? metrics.spaceAdvance : (metrics.glyphs[ch] ?? metrics.defaultAdvance);
    width += advance * fontSize;
  }
  return width;
}

// Greedy word wrap: a word joins the current line only if the widened line
// still fits maxWidth. Deterministic for a fixed metrics fixture.
export function layoutText(
  text: string,
  fontSize: number,
  metrics: GlyphMetrics,
  maxWidth: number,
): LayoutLine[] {
  const lineHeight = metrics.lineHeight * fontSize;
  const lines: LayoutLine[] = [];
  let current = '';
  const push = () => {
    lines.push({ text: current, width: measureText(current, fontSize, metrics), y: lines.length * lineHeight });
  };
  for (const word of text.split(' ')) {
    if (word === '') continue;
    const candidate = current === '' ? word : `${current} ${word}`;
    if (current !== '' && measureText(candidate, fontSize, metrics) > maxWidth) {
      push();
      current = word;
    } else {
      current = candidate;
    }
  }
  if (current !== '') push();
  return lines;
}

export function layout(node: LayoutNode, metrics: GlyphMetrics, x = 0, y = 0, availableWidth = Infinity): LayoutBox {
  if (node.type === 'text') {
    const fontSize = node.style?.fontSize ?? 16;
    const width = node.style?.width ?? availableWidth;
    const lines = layoutText(node.text, fontSize, metrics, width);
    const height = node.style?.height ?? lines.length * metrics.lineHeight * fontSize;
    return { id: node.id, x, y, width, height, lines };
  }
  const style = node.style ?? {};
  const padding = style.padding ?? 0;
  const width = style.width ?? availableWidth;
  const contentWidth = width - 2 * padding;
  const children: LayoutBox[] = [];
  let cursorY = y + padding;
  for (const child of node.children ?? []) {
    const margin = child.style?.margin ?? 0;
    cursorY += margin;
    const box = layout(child, metrics, x + padding + margin, cursorY, contentWidth - 2 * margin);
    children.push(box);
    cursorY += box.height + margin;
  }
  const height = style.height ?? cursorY - y + padding;
  return { id: node.id, x, y, width, height, children };
}

// Applies sampled animation props (e.g. { 'root.width': 160 }) to a node
// tree, returning a new tree. This is how the motion engine drives layout
// without any shared mutable state.
export function applyProps(node: LayoutNode, props: Record<string, number>): LayoutNode {
  const id = node.id;
  const style: Style = { ...node.style };
  if (id !== undefined) {
    for (const key of Object.keys(style) as (keyof Style)[]) {
      const prop = props[`${id}.${key}`];
      if (prop !== undefined) style[key] = prop;
    }
  }
  if (node.type === 'text') return { ...node, style };
  return {
    ...node,
    style,
    children: (node.children ?? []).map((child) => applyProps(child, props)),
  };
}
