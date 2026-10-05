import { NodeSpec, Tree, TreeNode } from './types';

export const MAX_NODES = 15;

export function buildTree(specs: NodeSpec[], problems: string[]): Tree | null {
  const nodes = new Map<string, TreeNode>();
  for (const spec of specs) {
    if (nodes.has(spec.id)) {
      problems.push(`duplicate node id: ${spec.id}`);
      continue;
    }
    nodes.set(spec.id, {
      id: spec.id,
      parentId: spec.parent,
      children: [],
      grants: [...(spec.grants ?? [])],
      refs: [...(spec.refs ?? [])],
      fixed: spec.fixed ?? false,
      targetParent: spec.targetParent,
    });
  }
  if (nodes.size === 0) {
    problems.push('scenario has no nodes');
    return null;
  }
  if (nodes.size > MAX_NODES) {
    problems.push(`scenario has ${nodes.size} nodes, exceeds limit of ${MAX_NODES}`);
    return null;
  }
  const roots: string[] = [];
  for (const node of nodes.values()) {
    if (node.parentId === null) {
      roots.push(node.id);
      continue;
    }
    const parent = nodes.get(node.parentId);
    if (!parent) {
      problems.push(`node ${node.id} references missing parent ${node.parentId}`);
      continue;
    }
    parent.children.push(node.id);
  }
  if (roots.length !== 1) {
    problems.push(`expected exactly 1 root node, found ${roots.length}`);
    return null;
  }
  const visited = new Set<string>();
  const stack = [roots[0]];
  while (stack.length > 0) {
    const id = stack.pop()!;
    if (visited.has(id)) continue;
    visited.add(id);
    for (const child of nodes.get(id)!.children) stack.push(child);
  }
  if (visited.size !== nodes.size) {
    const unreachable = [...nodes.keys()].filter((id) => !visited.has(id));
    problems.push(`cycle or disconnected nodes detected: ${unreachable.join(', ')}`);
    return null;
  }
  return { rootId: roots[0], nodes };
}

export function effectiveGrants(tree: Tree): Map<string, Set<string>> {
  const result = new Map<string, Set<string>>();
  const walk = (id: string, inherited: Set<string>): void => {
    const node = tree.nodes.get(id)!;
    const eff = new Set(inherited);
    for (const grant of node.grants) eff.add(grant);
    result.set(id, eff);
    for (const child of node.children) walk(child, eff);
  };
  walk(tree.rootId, new Set());
  return result;
}

export function absolutePath(tree: Tree, id: string): string {
  const parts: string[] = [];
  let cur: string | null = id;
  while (cur !== null) {
    parts.unshift(cur);
    cur = tree.nodes.get(cur)!.parentId;
  }
  return '/' + parts.join('/');
}

/**
 * POSIX 文件语义：相对路径从引用方的父节点开始解析，
 * 绝对路径（以 / 开头）从根节点开始。路径段即节点 id。
 */
export function resolveRef(tree: Tree, ownerId: string, ref: string): string | null {
  const owner = tree.nodes.get(ownerId);
  if (!owner) return null;
  let cur: string | null;
  let rest = ref;
  if (ref.startsWith('/')) {
    cur = tree.rootId;
    rest = ref.slice(1);
  } else {
    cur = owner.parentId ?? owner.id;
  }
  if (rest === '') return cur;
  for (const seg of rest.split('/')) {
    if (seg === '' || seg === '.') continue;
    if (cur === null) return null;
    if (seg === '..') {
      cur = tree.nodes.get(cur)!.parentId ?? cur;
      continue;
    }
    const next: string | undefined = tree.nodes.get(cur)!.children.find((c) => c === seg);
    if (next === undefined) return null;
    cur = next;
  }
  return cur;
}
