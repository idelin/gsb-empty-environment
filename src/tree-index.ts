import { joinPointer } from './locations.ts';
import type { NodeId, TreeDoc } from './types.ts';

export interface DocIndex {
  doc: TreeDoc;
  parentOf: Map<NodeId, { parent: NodeId; key: string }>;
  pointerOf: Map<NodeId, string>;
}

/** Build parent/pointer maps from the ORIGINAL edges of a parsed tree. */
export function indexDoc(tree: TreeDoc): DocIndex {
  const parentOf = new Map<NodeId, { parent: NodeId; key: string }>();
  const pointerOf = new Map<NodeId, string>();

  const visit = (id: NodeId, pointer: string): void => {
    const node = tree.nodes[id];
    if (!node) return;
    pointerOf.set(id, pointer);
    if (node.kind !== 'object') return;
    for (const edge of node.edges) {
      if (!tree.nodes[edge.child]) continue;
      parentOf.set(edge.child, { parent: id, key: edge.key });
      visit(edge.child, joinPointer(pointer, edge.key));
    }
  };

  visit(tree.root, '');
  return { doc: tree, parentOf, pointerOf };
}

/** The signed node plus all of its ORIGINAL descendants. */
export function originalSubtreeIds(tree: TreeDoc, rootId: NodeId): Set<NodeId> {
  const result = new Set<NodeId>();
  const stack: NodeId[] = [rootId];
  while (stack.length > 0) {
    const id = stack.pop() as NodeId;
    if (result.has(id)) continue;
    const node = tree.nodes[id];
    if (!node) continue;
    result.add(id);
    if (node.kind === 'object') {
      for (const edge of node.edges) stack.push(edge.child);
    }
  }
  return result;
}

export function findByPointer(
  tree: TreeDoc,
  pointer: string,
): { nodeId: NodeId; parentId?: NodeId; key?: string } | undefined {
  if (pointer === '') return { nodeId: tree.root };
  const segments = pointer.split('/').slice(1).map((s) => s.replaceAll('~1', '/').replaceAll('~0', '~'));
  let currentId = tree.root;
  let parentId: NodeId | undefined;
  let key: string | undefined;
  for (const segment of segments) {
    const node = tree.nodes[currentId];
    if (!node || node.kind !== 'object') return undefined;
    const edge = node.edges.find((e) => e.key === segment);
    if (!edge) return undefined;
    parentId = currentId;
    key = edge.key;
    currentId = edge.child;
  }
  return { nodeId: currentId, parentId, key };
}

/** Stable structural signature of intrinsic node content (ids excluded). */
export function intrinsicKey(tree: TreeDoc, id: NodeId): string {
  const node = tree.nodes[id];
  if (!node) return 'missing';
  switch (node.kind) {
    case 'object':
      return `object{${node.edges.map((e) => e.key).join(',')}}`;
    case 'value':
      return `value:${JSON.stringify(node.value)}`;
    case 'null':
      return 'null';
    case 'tombstone':
      return 'tombstone';
    case 'ref':
      return `ref:${node.template}`;
  }
}
