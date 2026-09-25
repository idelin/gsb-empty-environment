import type { Axis, Axes, AxisTransform, Vec2 } from './types.ts';

export interface GlyphMetrics {
  lineHeight: number;
  advance: number;
}

export interface ContentFixture {
  lines: Array<{ chars: number; repeat: number }>;
}

export interface NodeFixture {
  id: string;
  parent: string | null;
  rect: [number, number, number, number];
  axes: Axes;
  snap: number;
  transform: AxisTransform;
  content: ContentFixture;
}

export interface SceneFixture {
  glyphMetrics: GlyphMetrics;
  nodes: NodeFixture[];
}

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

/**
 * Content size is derived purely from the fixture's glyph metrics
 * (line height x line count, advance x max line length). No browser
 * layout engine is involved anywhere in the core computation.
 */
export function measureContent(content: ContentFixture, metrics: GlyphMetrics): Vec2 {
  let width = 0;
  let height = 0;
  for (const line of content.lines) {
    width = Math.max(width, line.chars * metrics.advance);
    height += line.repeat * metrics.lineHeight;
  }
  return { x: width, y: height };
}

export class ScrollNode {
  readonly id: string;
  readonly rect: Rect;
  readonly axes: Axes;
  readonly snapStep: number;
  readonly transform: AxisTransform;
  readonly content: Vec2;
  parent: ScrollNode | null = null;
  readonly children: ScrollNode[] = [];
  readonly offset: Vec2 = { x: 0, y: 0 };
  /** Sub-snap remainders kept per axis so fractional input is never lost. */
  readonly residual: Vec2 = { x: 0, y: 0 };

  constructor(fixture: NodeFixture, metrics: GlyphMetrics) {
    this.id = fixture.id;
    const [x, y, w, h] = fixture.rect;
    this.rect = { x, y, w, h };
    this.axes = fixture.axes;
    this.snapStep = fixture.snap;
    this.transform = fixture.transform;
    this.content = measureContent(fixture.content, metrics);
  }

  allows(axis: Axis): boolean {
    return this.axes === 'both' || this.axes === axis;
  }

  maxScroll(axis: Axis): number {
    const extent = axis === 'x' ? this.content.x - this.rect.w : this.content.y - this.rect.h;
    return Math.max(0, extent);
  }

  containsPoint(point: Vec2): boolean {
    return (
      point.x >= this.rect.x &&
      point.x < this.rect.x + this.rect.w &&
      point.y >= this.rect.y &&
      point.y < this.rect.y + this.rect.h
    );
  }
}

export interface ScrollScene {
  root: ScrollNode;
  nodes: Map<string, ScrollNode>;
}

export function buildScene(fixture: SceneFixture): ScrollScene {
  const nodes = new Map<string, ScrollNode>();
  for (const nodeFixture of fixture.nodes) {
    nodes.set(nodeFixture.id, new ScrollNode(nodeFixture, fixture.glyphMetrics));
  }
  let root: ScrollNode | null = null;
  for (const nodeFixture of fixture.nodes) {
    const node = nodes.get(nodeFixture.id)!;
    if (nodeFixture.parent === null) {
      root = node;
      continue;
    }
    const parent = nodes.get(nodeFixture.parent);
    if (!parent) throw new Error(`unknown parent ${nodeFixture.parent}`);
    node.parent = parent;
    parent.children.push(node);
  }
  if (!root) throw new Error('fixture has no root node');
  return { root, nodes };
}

/** Deepest node containing the point, then every ancestor up to the root. */
export function hitChain(root: ScrollNode, point: Vec2): ScrollNode[] {
  let deepest: ScrollNode | null = null;
  const visit = (node: ScrollNode): void => {
    if (!node.containsPoint(point)) return;
    deepest = node;
    for (const child of node.children) visit(child);
  };
  visit(root);
  if (!deepest) return [];
  return pathToRoot(deepest);
}

export function pathToRoot(node: ScrollNode): ScrollNode[] {
  const chain: ScrollNode[] = [];
  let current: ScrollNode | null = node;
  while (current) {
    chain.push(current);
    current = current.parent;
  }
  return chain;
}
