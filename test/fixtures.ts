import type { GlyphMetrics, TreeNode } from '../src/types.ts';

export function node(init: Partial<TreeNode> & { id: string }): TreeNode {
  return { attrs: {}, children: [], ...init };
}

export const metrics: GlyphMetrics = { charWidth: 1 };
