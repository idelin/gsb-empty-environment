import { measureText, type GlyphMetrics } from "./glyphs.ts";
import type { NodeId, NodeTree } from "./tree.ts";

export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface LayoutOptions {
  padding: number;
  gap: number;
  indent: number;
}

/**
 * 确定性块布局:节点按文档序垂直堆叠,宽度由字形度量推出。
 * 全部为核心纯计算,不借浏览器布局引擎。
 */
export function layoutTree(
  tree: NodeTree,
  metrics: GlyphMetrics,
  opts: LayoutOptions,
): Map<NodeId, Rect> {
  const rects = new Map<NodeId, Rect>();
  let cursor = 0;
  const visit = (id: NodeId, depth: number): void => {
    const node = tree.get(id);
    if (!node || node.hidden) return;
    const width = measureText(node.text, metrics) + opts.padding * 2;
    const height = metrics.lineHeight + opts.padding * 2;
    rects.set(id, { x: depth * opts.indent, y: cursor, width, height });
    cursor += height + opts.gap;
    for (const child of node.children) visit(child, depth + 1);
  };
  visit(tree.rootId, 0);
  return rects;
}

/** 命中测试:文档序靠后者(视觉上层)优先。 */
export function hitTest(
  rects: Map<NodeId, Rect>,
  order: NodeId[],
  x: number,
  y: number,
): NodeId | null {
  for (let i = order.length - 1; i >= 0; i--) {
    const rect = rects.get(order[i]);
    if (rect && x >= rect.x && x < rect.x + rect.width && y >= rect.y && y < rect.y + rect.height) {
      return order[i];
    }
  }
  return null;
}
