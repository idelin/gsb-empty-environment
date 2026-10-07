import type { FlatRow, TreeNode } from './types.ts';

/**
 * Depth-first projection of the tree onto the visible row list.
 * A row is visible iff every ancestor is expanded; children of a
 * collapsed row never appear, so navigation can never land on them.
 */
export function flattenVisible(
  nodes: TreeNode[],
  parentId: string | null = null,
  depth = 0,
  out: FlatRow[] = [],
): FlatRow[] {
  for (const node of nodes) {
    out.push({ id: node.id, depth, parentId, node });
    if (node.expanded && node.children) {
      flattenVisible(node.children, node.id, depth + 1, out);
    }
  }
  return out;
}
