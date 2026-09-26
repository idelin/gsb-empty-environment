/**
 * 纯函数布局引擎：节点树与字形度量均由夹具（fixture）给出，
 * 核心计算不依赖浏览器布局引擎。
 */

export interface FontMetrics {
  /** 每个字形的步进宽度（单位：px，按 size=16 标定）。 */
  advance: Record<string, number>;
  defaultAdvance: number;
  lineHeight: number;
}

export interface GlyphMetrics {
  fonts: Record<string, FontMetrics>;
}

export type Node =
  | {
      kind: "box";
      id: string;
      direction: "row" | "column";
      padding: number;
      gap: number;
      children: Node[];
    }
  | { kind: "text"; id: string; text: string; font: string; size: number };

export interface LayoutBox {
  id: string;
  x: number;
  y: number;
  width: number;
  height: number;
  children: LayoutBox[];
}

/** 文本测量：逐字查字形步进表，缺失字形用默认步进。 */
export function measureText(
  text: string,
  font: string,
  size: number,
  metrics: GlyphMetrics,
): { width: number; height: number } {
  const fm = metrics.fonts[font];
  if (!fm) throw new Error(`unknown font: ${font}`);
  const scale = size / 16;
  let width = 0;
  for (const ch of text) {
    const adv = fm.advance[ch] ?? fm.defaultAdvance;
    width += adv * scale;
  }
  return { width, height: fm.lineHeight * scale };
}

/** 自底向上量尺寸，自顶向下排位置。 */
export function layout(node: Node, metrics: GlyphMetrics, origin = { x: 0, y: 0 }): LayoutBox {
  const natural = measure(node, metrics);
  return place(node, metrics, origin.x, origin.y, natural.width, natural.height);
}

function measure(node: Node, metrics: GlyphMetrics): { width: number; height: number } {
  if (node.kind === "text") {
    return measureText(node.text, node.font, node.size, metrics);
  }
  const childSizes = node.children.map((c) => measure(c, metrics));
  const gaps = Math.max(0, node.children.length - 1) * node.gap;
  if (node.direction === "row") {
    return {
      width: node.padding * 2 + childSizes.reduce((a, s) => a + s.width, 0) + gaps,
      height: node.padding * 2 + Math.max(0, ...childSizes.map((s) => s.height)),
    };
  }
  return {
    width: node.padding * 2 + Math.max(0, ...childSizes.map((s) => s.width)),
    height: node.padding * 2 + childSizes.reduce((a, s) => a + s.height, 0) + gaps,
  };
}

function place(
  node: Node,
  metrics: GlyphMetrics,
  x: number,
  y: number,
  width: number,
  height: number,
): LayoutBox {
  if (node.kind === "text") {
    return { id: node.id, x, y, width, height, children: [] };
  }
  const boxes: LayoutBox[] = [];
  let cursor = node.direction === "row" ? x + node.padding : y + node.padding;
  for (const child of node.children) {
    const size = measure(child, metrics);
    const cx = node.direction === "row" ? cursor : x + node.padding;
    const cy = node.direction === "row" ? y + node.padding : cursor;
    boxes.push(place(child, metrics, cx, cy, size.width, size.height));
    cursor += (node.direction === "row" ? size.width : size.height) + node.gap;
  }
  return { id: node.id, x, y, width, height, children: boxes };
}

/** 在布局结果中按 id 查找盒子。 */
export function findBox(root: LayoutBox, id: string): LayoutBox | null {
  if (root.id === id) return root;
  for (const child of root.children) {
    const found = findBox(child, id);
    if (found) return found;
  }
  return null;
}

/** 节点 id 集合，供执行器回放时校验目标节点是否存在。 */
export function nodeIds(node: Node): Set<string> {
  const ids = new Set<string>([node.id]);
  if (node.kind === "box") {
    for (const child of node.children) {
      for (const id of nodeIds(child)) ids.add(id);
    }
  }
  return ids;
}
