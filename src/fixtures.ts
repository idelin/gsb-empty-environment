import type { GlyphMetrics } from './metrics.ts';
import type { NodeId, TreeNode, TreeState } from './types.ts';

/**
 * 夹具节点树：
 *   a (Alpha)
 *   ├── a1 (Alpha One)
 *   └── a2 (Alpha Two)
 *   b (Beta)
 *   └── b1 (Beta One)
 *   c (Gamma)
 */
export function fixtureTree(): TreeState {
  const nodes: Record<NodeId, TreeNode> = {};
  const mk = (id: NodeId, parentId: NodeId | null, childIds: NodeId[], label: string): void => {
    nodes[id] = { id, parentId, childIds, label, revision: 0 };
  };
  mk('a', null, ['a1', 'a2'], 'Alpha');
  mk('a1', 'a', [], 'Alpha One');
  mk('a2', 'a', [], 'Alpha Two');
  mk('b', null, ['b1'], 'Beta');
  mk('b1', 'b', [], 'Beta One');
  mk('c', null, [], 'Gamma');
  return { nodes, rootIds: ['a', 'b', 'c'], version: 0 };
}

/** 夹具字形度量：行高 24、缩进宽 16、各节点标签宽度（模拟已测字形） */
export const fixtureMetrics: GlyphMetrics = {
  rowHeight: 24,
  indentWidth: 16,
  labelWidths: { a: 41, a1: 68, a2: 70, b: 30, b1: 62, c: 46 },
};
