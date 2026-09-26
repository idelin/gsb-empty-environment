/** Pure, immutable tree operations. No DOM, no layout engine. */
import type { Location, MovePatch, NodeId, Tree, TreeNode } from './types.ts';

export function cloneTree(tree: Tree): Tree {
  const nodes: Record<NodeId, TreeNode> = {};
  for (const [id, n] of Object.entries(tree.nodes)) {
    nodes[id] = { id: n.id, parentId: n.parentId, label: n.label, children: [...n.children] };
  }
  return { rootId: tree.rootId, nodes };
}

export function locate(tree: Tree, nodeId: NodeId): Location {
  const node = tree.nodes[nodeId];
  if (!node || node.parentId === null) throw new Error(`node not found or is root: ${nodeId}`);
  const parent = tree.nodes[node.parentId];
  return { parentId: node.parentId, index: parent.children.indexOf(nodeId) };
}

export function isDescendant(tree: Tree, ancestorId: NodeId, nodeId: NodeId): boolean {
  let cur = tree.nodes[nodeId];
  while (cur && cur.parentId !== null) {
    if (cur.parentId === ancestorId) return true;
    cur = tree.nodes[cur.parentId];
  }
  return false;
}

export function subtreeIds(tree: Tree, nodeId: NodeId): NodeId[] {
  const out: NodeId[] = [];
  const walk = (id: NodeId) => {
    out.push(id);
    for (const c of tree.nodes[id].children) walk(c);
  };
  walk(nodeId);
  return out;
}

/** Validate a move against a tree. Returns an error string or null. */
export function validateMove(tree: Tree, nodeId: NodeId, to: Location): string | null {
  const node = tree.nodes[nodeId];
  if (!node) return `unknown node: ${nodeId}`;
  if (nodeId === tree.rootId) return 'cannot move root';
  const parent = tree.nodes[to.parentId];
  if (!parent) return `unknown target parent: ${to.parentId}`;
  if (to.parentId === nodeId || isDescendant(tree, nodeId, to.parentId)) {
    return 'cannot move a node into its own descendant';
  }
  // `to.index` is in post-removal coordinates.
  const from = locate(tree, nodeId);
  const capacity = from.parentId === to.parentId
    ? parent.children.length - 1
    : parent.children.length;
  if (to.index < 0 || to.index > capacity) return `index out of range: ${to.index}`;
  return null;
}

/** Apply a move, returning the new tree and the resulting patch. */
export function applyMove(tree: Tree, nodeId: NodeId, to: Location): { tree: Tree; patch: MovePatch } {
  const err = validateMove(tree, nodeId, to);
  if (err) throw new Error(`invalid move: ${err}`);
  const from = locate(tree, nodeId);
  const next = cloneTree(tree);
  next.nodes[from.parentId].children.splice(from.index, 1);
  next.nodes[to.parentId].children.splice(to.index, 0, nodeId);
  next.nodes[nodeId].parentId = to.parentId;
  return { tree: next, patch: { kind: 'move', nodeId, from, to } };
}

/** Apply an already-computed move patch (used for server broadcasts). */
export function applyMovePatch(tree: Tree, patch: MovePatch): Tree {
  const from = locate(tree, patch.nodeId);
  if (from.parentId !== patch.from.parentId || from.index !== patch.from.index) {
    throw new Error(`patch source mismatch for node ${patch.nodeId}`);
  }
  return applyMove(tree, patch.nodeId, patch.to).tree;
}

export function insertNode(tree: Tree, node: TreeNode, parentId: NodeId, index: number): Tree {
  const parent = tree.nodes[parentId];
  if (!parent) throw new Error(`unknown parent: ${parentId}`);
  if (tree.nodes[node.id]) throw new Error(`duplicate node: ${node.id}`);
  const next = cloneTree(tree);
  next.nodes[node.id] = { id: node.id, parentId, label: node.label, children: [] };
  next.nodes[parentId].children.splice(index, 0, node.id);
  return next;
}

export function removeSubtree(tree: Tree, nodeId: NodeId): Tree {
  const from = locate(tree, nodeId);
  const next = cloneTree(tree);
  for (const id of subtreeIds(tree, nodeId)) delete next.nodes[id];
  next.nodes[from.parentId].children.splice(from.index, 1);
  return next;
}

export interface FlatRow { id: NodeId; depth: number }

/** Visible rows in depth-first order (the list is always fully expanded). */
export function flatten(tree: Tree): FlatRow[] {
  const rows: FlatRow[] = [];
  const walk = (id: NodeId, depth: number) => {
    rows.push({ id, depth });
    for (const c of tree.nodes[id].children) walk(c, depth + 1);
  };
  for (const c of tree.nodes[tree.rootId].children) walk(c, 0);
  return rows;
}

export function depthOf(tree: Tree, nodeId: NodeId): number {
  let depth = 0;
  let cur = tree.nodes[nodeId];
  while (cur.parentId !== null && cur.parentId !== tree.rootId) {
    depth += 1;
    cur = tree.nodes[cur.parentId];
  }
  return depth;
}

export function formatTree(tree: Tree): string {
  const lines: string[] = [];
  for (const row of flatten(tree)) {
    lines.push(`${'  '.repeat(row.depth)}${tree.nodes[row.id].label} [${row.id}]`);
  }
  return lines.join('\n');
}
