import type { Op, TreeNode } from './types.ts';
import { TreeError, validateTree } from './validate.ts';
import { deepClone } from './util.ts';

/**
 * Applies a patch produced by diffTrees to `oldInput` and returns the new
 * tree. The input is not mutated. Reference errors (unknown ids/parents)
 * throw a TreeError located at the offending op (`/ops/<n>`).
 *
 * Application order: updates -> create inserted nodes -> detach removed
 * subtree roots -> detach moved nodes -> place inserts/moves at their final
 * indexes. Because inserts are parent-first and every index refers to the
 * final layout, no step ever touches a dangling reference.
 */
export function applyPatch(oldInput: TreeNode | null, ops: readonly Op[]): TreeNode | null {
  const oldRoot = oldInput === null ? null : validateTree(oldInput);
  const root = oldRoot === null ? null : deepClone(oldRoot);
  // Virtual holder for the root level so `parentId: null` is uniform.
  const holder: TreeNode[] = root === null ? [] : [root];

  const byId = new Map<string, TreeNode>();
  const parentOf = new Map<string, TreeNode | null>();
  const register = (node: TreeNode, parent: TreeNode | null): void => {
    byId.set(node.id, node);
    parentOf.set(node.id, parent);
    for (const child of node.children ?? []) register(child, node);
  };
  if (root !== null) register(root, null);

  const opPath = (i: number): string => `/ops/${i}`;

  // 1. Attribute updates on retained nodes.
  ops.forEach((op, i) => {
    if (op.op !== 'update') return;
    const target = byId.get(op.id);
    if (target === undefined) {
      throw new TreeError(`update references unknown node id "${op.id}"`, opPath(i));
    }
    const attrs = { ...(target.attrs ?? {}) };
    for (const key of op.unset) delete attrs[key];
    for (const key of Object.keys(op.set)) attrs[key] = deepClone(op.set[key]);
    if (Object.keys(attrs).length > 0) target.attrs = attrs;
    else delete target.attrs;
  });

  // 2. Create inserted nodes (attachment happens in step 5).
  ops.forEach((op, i) => {
    if (op.op !== 'insert') return;
    if (byId.has(op.id)) {
      throw new TreeError(`insert duplicates existing node id "${op.id}"`, opPath(i));
    }
    const node: TreeNode = { id: op.id };
    if (Object.keys(op.attrs).length > 0) node.attrs = deepClone(op.attrs);
    byId.set(op.id, node);
  });

  // 3. Resolve placements (inserts + moves) against existing parents.
  const incoming = new Map<TreeNode | null, { node: TreeNode; index: number }[]>();
  const incomingIds = new Set<string>();
  ops.forEach((op, i) => {
    if (op.op !== 'insert' && op.op !== 'move') return;
    const node = byId.get(op.id);
    if (node === undefined) {
      throw new TreeError(`${op.op} references unknown node id "${op.id}"`, opPath(i));
    }
    let parent: TreeNode | null = null;
    if (op.parentId !== null) {
      const found = byId.get(op.parentId);
      if (found === undefined) {
        throw new TreeError(`${op.op} references unknown parent id "${op.parentId}"`, opPath(i));
      }
      parent = found;
    }
    const list = incoming.get(parent) ?? [];
    list.push({ node, index: op.index });
    incoming.set(parent, list);
    incomingIds.add(op.id);
  });

  const detach = (node: TreeNode): void => {
    const parent = parentOf.get(node.id) ?? null;
    const siblings = parent === null ? holder : (parent.children ?? []);
    const at = siblings.indexOf(node);
    if (at >= 0) siblings.splice(at, 1);
  };

  // 4. Detach removed subtree roots (children moved away stay reachable,
  //    they are detached from the discarded subtree in the next step).
  ops.forEach((op, i) => {
    if (op.op !== 'remove') return;
    const node = byId.get(op.id);
    if (node === undefined) {
      throw new TreeError(`remove references unknown node id "${op.id}"`, opPath(i));
    }
    detach(node);
  });

  // 5. Detach moved/inserted nodes from wherever they currently sit, then
  //    rebuild every parent that received placements at final indexes.
  for (const placements of incoming.values()) {
    for (const { node } of placements) detach(node);
  }
  for (const [parent, placements] of incoming) {
    const current = parent === null ? holder : (parent.children ?? []);
    const staying = current.filter((child) => !incomingIds.has(child.id));
    const sorted = [...placements].sort((a, b) => a.index - b.index);
    const merged: TreeNode[] = [];
    let s = 0;
    for (const { node, index } of sorted) {
      while (merged.length < index && s < staying.length) merged.push(staying[s++]);
      merged.push(node);
    }
    while (s < staying.length) merged.push(staying[s++]);
    if (parent === null) {
      holder.length = 0;
      holder.push(...merged);
    } else if (merged.length > 0) {
      parent.children = merged;
    } else {
      delete parent.children;
    }
  }

  return holder.length > 0 ? holder[0] : null;
}
