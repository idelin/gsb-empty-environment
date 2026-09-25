import type { JsonValue, Op, TreeNode } from './types.ts';
import { validateTree } from './validate.ts';
import { attrsOf, childrenOf, compareStrings, jsonEquals } from './util.ts';

interface Entry {
  node: TreeNode;
  parentId: string | null;
  index: number;
}

function indexTree(root: TreeNode | null): Map<string, Entry> {
  const index = new Map<string, Entry>();
  const visit = (node: TreeNode, parentId: string | null, position: number): void => {
    index.set(node.id, { node, parentId, index: position });
    childrenOf(node).forEach((child, i) => visit(child, node.id, i));
  };
  if (root !== null) visit(root, null, 0);
  return index;
}

/** parentId (null = root level) -> ordered child ids. */
function childrenMap(root: TreeNode | null): Map<string | null, string[]> {
  const map = new Map<string | null, string[]>();
  const visit = (node: TreeNode, parentId: string | null): void => {
    const list = map.get(parentId) ?? [];
    list.push(node.id);
    map.set(parentId, list);
    for (const child of childrenOf(node)) visit(child, node.id);
  };
  if (root !== null) visit(root, null);
  return map;
}

/**
 * Indices of one longest strictly-increasing subsequence of `seq`.
 * Deterministic: same input always yields the same subsequence.
 */
function lisIndices(seq: number[]): Set<number> {
  const tails: number[] = [];
  const prev: number[] = new Array(seq.length).fill(-1);
  for (let i = 0; i < seq.length; i++) {
    let lo = 0;
    let hi = tails.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (seq[tails[mid]] < seq[i]) lo = mid + 1;
      else hi = mid;
    }
    if (lo > 0) prev[i] = tails[lo - 1];
    if (lo === tails.length) tails.push(i);
    else tails[lo] = i;
  }
  const kept = new Set<number>();
  let cursor = tails.length === 0 ? -1 : tails[tails.length - 1];
  while (cursor >= 0) {
    kept.add(cursor);
    cursor = prev[cursor];
  }
  return kept;
}

function byId(a: { id: string }, b: { id: string }): number {
  return compareStrings(a.id, b.id);
}

/**
 * Computes the minimal patch transforming `oldInput` into `newInput`.
 *
 * - Identity is the stable `id`: a node present in both trees is never
 *   removed/re-inserted, only moved and/or updated.
 * - Removes are emitted only for the topmost deleted node of each deleted
 *   subtree, so children moved out beforehand are never deleted twice.
 * - Inserts are emitted in pre-order (parents before children); updates,
 *   moves and removes are sorted by id. The result is fully determined by
 *   the semantic content of the inputs, not by JSON key order.
 */
export function diffTrees(oldInput: TreeNode | null, newInput: TreeNode | null): Op[] {
  const oldRoot = oldInput === null ? null : validateTree(oldInput);
  const newRoot = newInput === null ? null : validateTree(newInput);
  const oldIndex = indexTree(oldRoot);
  const newIndex = indexTree(newRoot);

  const inserts: Op[] = [];
  const updates: Op[] = [];
  const moves: Op[] = [];
  const removes: Op[] = [];

  // Inserts: pre-order traversal guarantees parents are created before children.
  const visitNew = (node: TreeNode, parentId: string | null, position: number): void => {
    if (!oldIndex.has(node.id)) {
      inserts.push({ op: 'insert', id: node.id, parentId, index: position, attrs: { ...attrsOf(node) } });
    }
    childrenOf(node).forEach((child, i) => visitNew(child, node.id, i));
  };
  if (newRoot !== null) visitNew(newRoot, null, 0);

  // Attribute updates and cross-parent moves for retained nodes.
  for (const [id, entry] of newIndex) {
    const prev = oldIndex.get(id);
    if (!prev) continue;

    const oldAttrs = attrsOf(prev.node);
    const newAttrs = attrsOf(entry.node);
    const set: { [key: string]: JsonValue } = {};
    const unset: string[] = [];
    for (const key of Object.keys(newAttrs).sort(compareStrings)) {
      if (!(key in oldAttrs) || !jsonEquals(oldAttrs[key], newAttrs[key])) set[key] = newAttrs[key];
    }
    for (const key of Object.keys(oldAttrs).sort(compareStrings)) {
      if (!(key in newAttrs)) unset.push(key);
    }
    if (Object.keys(set).length > 0 || unset.length > 0) {
      updates.push({ op: 'update', id, set, unset });
    }

    if (prev.parentId !== entry.parentId) {
      moves.push({ op: 'move', id, parentId: entry.parentId, index: entry.index });
    }
  }

  // Same-parent reorders: move the minimal set (complement of a LIS of the
  // retained children), so untouched siblings keep their relative order.
  const oldChildren = childrenMap(oldRoot);
  const newChildren = childrenMap(newRoot);
  for (const [parentId, newKids] of newChildren) {
    const oldKids = oldChildren.get(parentId) ?? [];
    const retained = newKids.filter((id) => {
      const prev = oldIndex.get(id);
      return prev !== undefined && prev.parentId === parentId;
    });
    if (retained.length < 2) continue;
    const oldPosition = new Map(oldKids.map((id, i) => [id, i]));
    const seq = retained.map((id) => oldPosition.get(id) as number);
    const kept = lisIndices(seq);
    retained.forEach((id, i) => {
      if (!kept.has(i)) {
        const entry = newIndex.get(id) as Entry;
        moves.push({ op: 'move', id, parentId, index: entry.index });
      }
    });
  }

  // Removes: only the topmost node of each deleted subtree.
  for (const [id, entry] of oldIndex) {
    if (newIndex.has(id)) continue;
    const parentAlsoDeleted = entry.parentId !== null && !newIndex.has(entry.parentId);
    if (!parentAlsoDeleted) removes.push({ op: 'remove', id });
  }

  updates.sort(byId);
  moves.sort(byId);
  removes.sort(byId);
  return [...inserts, ...updates, ...moves, ...removes];
}
