// Pure state transitions. Every op validates against the current state and
// returns a fresh HeaderState; invalid ops throw before anything mutates.
import type { GroupNode, HeaderState, LeafNode } from './types.ts';

export type HeaderOp =
  | { type: 'hide'; columnIds: string[] }
  | { type: 'show'; columnIds: string[] }
  | { type: 'collapse'; groupId: string }
  | { type: 'expand'; groupId: string }
  | { type: 'moveColumn'; columnId: string; toGroupId: string; index?: number };

function cloneState(state: HeaderState): HeaderState {
  return {
    root: structuredClone(state.root),
    columns: state.columns,
    hidden: new Set(state.hidden),
    collapsed: new Set(state.collapsed),
  };
}

export function findGroup(root: GroupNode, groupId: string): GroupNode | null {
  if (root.id === groupId) return root;
  for (const child of root.children) {
    if (child.kind === 'group') {
      const hit = findGroup(child, groupId);
      if (hit) return hit;
    }
  }
  return null;
}

export function findLeafParent(
  root: GroupNode,
  columnId: string,
): { parent: GroupNode; index: number } | null {
  for (let i = 0; i < root.children.length; i++) {
    const child = root.children[i];
    if (child.kind === 'leaf' && child.columnId === columnId) {
      return { parent: root, index: i };
    }
    if (child.kind === 'group') {
      const hit = findLeafParent(child, columnId);
      if (hit) return hit;
    }
  }
  return null;
}

export function listGroupIds(root: GroupNode): string[] {
  const ids: string[] = [];
  const walk = (node: GroupNode): void => {
    ids.push(node.id);
    for (const child of node.children) {
      if (child.kind === 'group') walk(child);
    }
  };
  walk(root);
  return ids;
}

export function applyOp(state: HeaderState, op: HeaderOp): HeaderState {
  switch (op.type) {
    case 'hide': {
      for (const id of op.columnIds) {
        if (!state.columns.has(id)) throw new Error(`unknown column: ${id}`);
      }
      const next = cloneState(state);
      for (const id of op.columnIds) next.hidden.add(id);
      return next;
    }
    case 'show': {
      for (const id of op.columnIds) {
        if (!state.columns.has(id)) throw new Error(`unknown column: ${id}`);
      }
      const next = cloneState(state);
      for (const id of op.columnIds) next.hidden.delete(id);
      return next;
    }
    case 'collapse': {
      if (!findGroup(state.root, op.groupId)) {
        throw new Error(`unknown group: ${op.groupId}`);
      }
      const next = cloneState(state);
      next.collapsed.add(op.groupId);
      return next;
    }
    case 'expand': {
      if (!findGroup(state.root, op.groupId)) {
        throw new Error(`unknown group: ${op.groupId}`);
      }
      const next = cloneState(state);
      next.collapsed.delete(op.groupId);
      return next;
    }
    case 'moveColumn': {
      if (!state.columns.has(op.columnId)) {
        throw new Error(`unknown column: ${op.columnId}`);
      }
      const target = findGroup(state.root, op.toGroupId);
      if (!target) throw new Error(`unknown group: ${op.toGroupId}`);
      const from = findLeafParent(state.root, op.columnId);
      if (!from) throw new Error(`column not in tree: ${op.columnId}`);
      const next = cloneState(state);
      const nextFrom = findLeafParent(next.root, op.columnId)!;
      nextFrom.parent.children.splice(nextFrom.index, 1);
      const nextTarget = findGroup(next.root, op.toGroupId)!;
      const leaf: LeafNode = { kind: 'leaf', columnId: op.columnId };
      const index = op.index === undefined
        ? nextTarget.children.length
        : Math.max(0, Math.min(op.index, nextTarget.children.length));
      nextTarget.children.splice(index, 0, leaf);
      return next;
    }
  }
}

export function applyOps(state: HeaderState, ops: HeaderOp[]): HeaderState {
  let current = state;
  for (const op of ops) current = applyOp(current, op);
  return current;
}
