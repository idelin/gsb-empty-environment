// Minimal layout + hit testing over a fixture node tree.
// Text metrics come from the glyph-metrics fixture; no browser layout engine.

import type { Vec } from './geometry.ts';

export interface Rect { x: number; y: number; w: number; h: number; }

export interface GlyphMetricsFixture {
  unitsPerEm: number;
  glyphs: Record<string, { advance: number }>;
  defaultAdvance: number;
}

export interface NodeFixture {
  id: string;
  kind: 'box' | 'text';
  rect: Rect;
  text?: string;
  fontSize?: number;
  children?: NodeFixture[];
}

export interface LaidOutNode {
  id: string;
  kind: 'box' | 'text';
  /** Rect in canonical (root) coordinates. */
  rect: Rect;
  children: LaidOutNode[];
}

/** Width of a text run derived purely from glyph advances. */
export function measureText(text: string, fontSize: number, metrics: GlyphMetricsFixture): number {
  let units = 0;
  for (const ch of text) {
    const g = metrics.glyphs[ch];
    units += g ? g.advance : metrics.defaultAdvance;
  }
  return (units / metrics.unitsPerEm) * fontSize;
}

/** Lay out the fixture tree. Text nodes with w=0/h=0 get metrics-derived size. */
export function layoutTree(node: NodeFixture, metrics: GlyphMetricsFixture, offset: Vec = { x: 0, y: 0 }): LaidOutNode {
  const rect: Rect = {
    x: node.rect.x + offset.x,
    y: node.rect.y + offset.y,
    w: node.rect.w,
    h: node.rect.h,
  };
  if (node.kind === 'text' && node.text !== undefined) {
    const fontSize = node.fontSize ?? 16;
    if (rect.w === 0) rect.w = measureText(node.text, fontSize, metrics);
    if (rect.h === 0) rect.h = fontSize * 1.2;
  }
  const origin = { x: rect.x, y: rect.y };
  const children = (node.children ?? []).map((c) => layoutTree(c, metrics, origin));
  return { id: node.id, kind: node.kind, rect, children };
}

function contains(r: Rect, p: Vec): boolean {
  return p.x >= r.x && p.x <= r.x + r.w && p.y >= r.y && p.y <= r.y + r.h;
}

/** Deepest node containing the point (children painted above parents). */
export function hitTest(root: LaidOutNode, p: Vec): LaidOutNode | null {
  if (!contains(root.rect, p)) return null;
  for (let i = root.children.length - 1; i >= 0; i--) {
    const hit = hitTest(root.children[i]!, p);
    if (hit) return hit;
  }
  return root;
}
