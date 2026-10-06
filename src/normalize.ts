/**
 * 规范化：
 * - 合并相邻且格式集合相同（无序比较）的文本节点；
 * - 移除无锚点的空文本节点，带锚点的空节点必须保留；
 * - 带锚点的节点是合并屏障，保证锚点位置语义不变；
 * - 标记集合去重，链接不可嵌套（同节点保留首个 link）。
 * 返回原节点索引到新节点范围 [start, end) 的映射；被移除的节点映射为 null。
 * 规范化是幂等的：对结果再次执行不改变文档。
 */

import { normalizeMarks, sameMarks } from './model.js';
import type { Doc, DocNode } from './model.js';

export interface NodeRange {
  start: number;
  end: number;
}

export interface NormalizeResult {
  doc: Doc;
  map: Array<NodeRange | null>;
}

export function normalize(doc: Doc): NormalizeResult {
  const nodes: DocNode[] = [];
  const map: Array<NodeRange | null> = [];
  for (const node of doc.nodes) {
    const marks = normalizeMarks(node.marks);
    if (node.text.length === 0 && !node.anchor) {
      map.push(null);
      continue;
    }
    const last = nodes.length > 0 ? nodes[nodes.length - 1] : null;
    if (last && !last.anchor && !node.anchor && sameMarks(last.marks, marks)) {
      last.text += node.text;
      map.push({ start: nodes.length - 1, end: nodes.length });
    } else {
      const fresh: DocNode = { id: node.id, text: node.text, marks };
      if (node.anchor) fresh.anchor = node.anchor;
      nodes.push(fresh);
      map.push({ start: nodes.length - 1, end: nodes.length });
    }
  }
  return { doc: { nodes }, map };
}
