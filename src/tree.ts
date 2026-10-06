export interface OutlineNode {
  id: string;
  children: OutlineNode[];
}

export interface FlatEntry {
  id: string;
  parentId: string | null;
  depth: number;
  /** Index within the parent's children array. */
  index: number;
}

/** Flatten a forest in pre-order (document order). */
export function flattenPreOrder(nodes: OutlineNode[]): FlatEntry[] {
  const out: FlatEntry[] = [];
  const walk = (list: OutlineNode[], parentId: string | null, depth: number): void => {
    list.forEach((node, index) => {
      out.push({ id: node.id, parentId, depth, index });
      walk(node.children, node.id, depth + 1);
    });
  };
  walk(nodes, null, 0);
  return out;
}

/** Map every node id to its parent id (null for roots). */
export function buildParentMap(nodes: OutlineNode[]): Map<string, string | null> {
  const map = new Map<string, string | null>();
  for (const entry of flattenPreOrder(nodes)) map.set(entry.id, entry.parentId);
  return map;
}

/**
 * Parent-chain check: true when ancestorId is a strict ancestor of id.
 * Walks from id up to the root, so cost is O(depth).
 */
export function isAncestor(
  parentMap: Map<string, string | null>,
  ancestorId: string,
  id: string,
): boolean {
  let cursor = parentMap.get(id);
  while (cursor !== undefined && cursor !== null) {
    if (cursor === ancestorId) return true;
    cursor = parentMap.get(cursor);
  }
  return false;
}

export function findNode(nodes: OutlineNode[], id: string): OutlineNode | undefined {
  for (const node of nodes) {
    if (node.id === id) return node;
    const hit = findNode(node.children, id);
    if (hit) return hit;
  }
  return undefined;
}

export function childrenOf(
  nodes: OutlineNode[],
  parentId: string | null,
): OutlineNode[] | undefined {
  if (parentId === null) return nodes;
  return findNode(nodes, parentId)?.children;
}
