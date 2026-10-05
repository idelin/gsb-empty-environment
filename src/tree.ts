import type { TreeNode, Path } from './types.ts';

export function clone(node: TreeNode): TreeNode {
  const out: TreeNode = { type: node.type, children: node.children.map(clone) };
  if (node.value !== undefined) out.value = node.value;
  if (node.id !== undefined) out.id = node.id;
  return out;
}

/** Structural equality: type, value, id and ordered children. */
export function treeEquals(a: TreeNode, b: TreeNode): boolean {
  if (a.type !== b.type || a.value !== b.value || a.id !== b.id) return false;
  if (a.children.length !== b.children.length) return false;
  for (let i = 0; i < a.children.length; i++) {
    if (!treeEquals(a.children[i], b.children[i])) return false;
  }
  return true;
}

export function countNodes(node: TreeNode): number {
  let n = 1;
  for (const c of node.children) n += countNodes(c);
  return n;
}

export interface TreeIndex {
  /** Preorder list of nodes; position in this array is the canonical node index. */
  nodes: TreeNode[];
  /** parent[i] = index of the parent of node i, or -1 for the root. */
  parent: number[];
  /** childPos[i] = position of node i among its parent's children (0 for root). */
  childPos: number[];
  /** size[i] = number of nodes in the subtree rooted at node i. */
  size: number[];
  /** depth[i] = depth of node i (root = 0). */
  depth: number[];
  /** childrenIdx[i] = indices of the children of node i, in order. */
  childrenIdx: number[][];
  /** Breadth-first order of node indices (parents always precede children). */
  bfs: number[];
}

export function indexTree(root: TreeNode): TreeIndex {
  const nodes: TreeNode[] = [];
  const parent: number[] = [];
  const childPos: number[] = [];
  const size: number[] = [];
  const depth: number[] = [];
  const walk = (node: TreeNode, p: number, pos: number, d: number): number => {
    const i = nodes.length;
    nodes.push(node);
    parent.push(p);
    childPos.push(pos);
    depth.push(d);
    size.push(1);
    let total = 1;
    for (let k = 0; k < node.children.length; k++) {
      total += walk(node.children[k], i, k, d + 1);
    }
    size[i] = total;
    return total;
  };
  walk(root, -1, 0, 0);
  const childrenIdx: number[][] = nodes.map(() => []);
  for (let i = 1; i < nodes.length; i++) childrenIdx[parent[i]].push(i);
  const bfs: number[] = [];
  const q: number[] = [0];
  while (q.length > 0) {
    const i = q.shift() as number;
    bfs.push(i);
    for (const c of childrenIdx[i]) q.push(c);
  }
  return { nodes, parent, childPos, size, depth, childrenIdx, bfs };
}

export function getAt(root: TreeNode, path: Path): TreeNode {
  let node = root;
  for (const k of path) {
    if (k < 0 || k >= node.children.length) {
      throw new Error(`invalid path [${path.join(',')}] at segment ${k}`);
    }
    node = node.children[k];
  }
  return node;
}

/** Structural hash of a subtree (type, value, id, ordered children). */
export function subtreeHash(node: TreeNode): string {
  const parts: string[] = ['('];
  parts.push(JSON.stringify(node.type));
  parts.push(JSON.stringify(node.value ?? null));
  parts.push(JSON.stringify(node.id ?? null));
  for (const c of node.children) parts.push(subtreeHash(c));
  parts.push(')');
  return parts.join('');
}
