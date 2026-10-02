/** Layout-free geometry: text measurement from a glyph-metrics fixture and
 *  hit testing over a plain node tree. No browser layout engine involved. */

export interface GlyphFixture {
  unitsPerEm: number;
  /** Advance width per character, in font units. */
  advances: Record<string, number>;
  /** Advance for characters not listed explicitly. */
  fallback: number;
}

export function measureText(text: string, px: number, fixture: GlyphFixture): number {
  let units = 0;
  for (const ch of text) units += fixture.advances[ch] ?? fixture.fallback;
  return (units * px) / fixture.unitsPerEm;
}

export interface UINode {
  id: string;
  x: number;
  y: number;
  w: number;
  h: number;
  children: UINode[];
}

export function box(id: string, x: number, y: number, w: number, h: number, children: UINode[] = []): UINode {
  return { id, x, y, w, h, children };
}

/** A text run as a fixed box: width from the glyph fixture, height from line height. */
export function textBox(id: string, x: number, y: number, text: string, px: number, lineHeight: number, fixture: GlyphFixture): UINode {
  return { id, x, y, w: measureText(text, px, fixture), h: lineHeight, children: [] };
}

/** Deepest node containing the point; later siblings paint on top. */
export function hitTest(node: UINode, x: number, y: number): string | undefined {
  for (let i = node.children.length - 1; i >= 0; i--) {
    const hit = hitTest(node.children[i], x, y);
    if (hit !== undefined) return hit;
  }
  if (x >= node.x && x < node.x + node.w && y >= node.y && y < node.y + node.h) return node.id;
  return undefined;
}
