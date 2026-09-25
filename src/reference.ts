// Independent reference implementation. The production derivation walks
// the tree recursively and emits spans; this reference never does that —
// it keeps flat per-group child lists, assigns every leaf a positional
// sort key, and rebuilds the visible leaf order by sorting keys. Any
// divergence between the two over an arbitrary edit sequence is a bug.
import type { HeaderOp } from './model.ts';
import type { GroupNode, HeaderState } from './types.ts';

type Child = { t: 'leaf'; id: string } | { t: 'group'; id: string };

export interface ReferenceColumn {
  columnId: string; // representative column
  ownerGroupId: string; // explicit ownership
}

interface FlatModel {
  children: Map<string, Child[]>;
  parentOf: Map<string, string>; // group id -> parent group id
  hidden: Set<string>;
  collapsed: Set<string>;
}

function buildFlat(state: HeaderState): FlatModel {
  const children = new Map<string, Child[]>();
  const parentOf = new Map<string, string>();
  const walk = (node: GroupNode, parentId: string | null): void => {
    const list: Child[] = node.children.map((child) =>
      child.kind === 'leaf'
        ? { t: 'leaf', id: child.columnId }
        : { t: 'group', id: child.id },
    );
    children.set(node.id, list);
    if (parentId !== null) parentOf.set(node.id, parentId);
    for (const child of node.children) {
      if (child.kind === 'group') walk(child, node.id);
    }
  };
  walk(state.root, null);
  return {
    children,
    parentOf,
    hidden: new Set(state.hidden),
    collapsed: new Set(state.collapsed),
  };
}

function applyToFlat(model: FlatModel, op: HeaderOp): void {
  switch (op.type) {
    case 'hide':
      for (const id of op.columnIds) model.hidden.add(id);
      return;
    case 'show':
      for (const id of op.columnIds) model.hidden.delete(id);
      return;
    case 'collapse':
      model.collapsed.add(op.groupId);
      return;
    case 'expand':
      model.collapsed.delete(op.groupId);
      return;
    case 'moveColumn': {
      for (const list of model.children.values()) {
        const at = list.findIndex((c) => c.t === 'leaf' && c.id === op.columnId);
        if (at >= 0) {
          list.splice(at, 1);
          break;
        }
      }
      const target = model.children.get(op.toGroupId);
      if (!target) throw new Error(`unknown group: ${op.toGroupId}`);
      const index = op.index === undefined
        ? target.length
        : Math.max(0, Math.min(op.index, target.length));
      target.splice(index, 0, { t: 'leaf', id: op.columnId });
      return;
    }
  }
}

/** Group-id chain from the root down to (and including) groupId. */
function groupChain(model: FlatModel, groupId: string): string[] {
  const chain: string[] = [];
  let cursor: string | undefined = groupId;
  while (cursor !== undefined) {
    chain.unshift(cursor);
    cursor = model.parentOf.get(cursor);
  }
  return chain;
}

function visibleOf(model: FlatModel, rootId: string): ReferenceColumn[] {
  // Assign every visible leaf a positional key = child indices root->leaf.
  const entries: { key: number[]; columnId: string; groupId: string }[] = [];
  const stack: { groupId: string; prefix: number[] }[] = [
    { groupId: rootId, prefix: [] },
  ];
  while (stack.length > 0) {
    const { groupId, prefix } = stack.pop()!;
    const list = model.children.get(groupId) ?? [];
    for (let i = list.length - 1; i >= 0; i--) {
      const child = list[i];
      if (child.t === 'leaf') {
        if (!model.hidden.has(child.id)) {
          entries.push({
            key: [...prefix, i],
            columnId: child.id,
            groupId,
          });
        }
      } else {
        stack.push({ groupId: child.id, prefix: [...prefix, i] });
      }
    }
  }
  entries.sort((a, b) => {
    const n = Math.min(a.key.length, b.key.length);
    for (let i = 0; i < n; i++) {
      if (a.key[i] !== b.key[i]) return a.key[i] - b.key[i];
    }
    return a.key.length - b.key.length;
  });

  // Collapse: keep only the first visible leaf per collapsed group; the
  // topmost collapsed ancestor owns the resulting single column.
  const result: ReferenceColumn[] = [];
  const seenCollapsed = new Set<string>();
  for (const entry of entries) {
    const chain = groupChain(model, entry.groupId);
    const collapsedOwner = chain.find((id) => model.collapsed.has(id));
    if (collapsedOwner === undefined) {
      result.push({ columnId: entry.columnId, ownerGroupId: entry.groupId });
    } else if (!seenCollapsed.has(collapsedOwner)) {
      seenCollapsed.add(collapsedOwner);
      result.push({ columnId: entry.columnId, ownerGroupId: collapsedOwner });
    }
  }
  return result;
}

/**
 * Rebuild the visible column sequence from scratch for an arbitrary edit
 * sequence: apply commit batches 0..upto-1 to a fresh flat model.
 */
export function referenceVisibleColumns(
  initial: HeaderState,
  batches: HeaderOp[][],
  upto: number,
): ReferenceColumn[] {
  const model = buildFlat(initial);
  for (let i = 0; i < upto; i++) {
    for (const op of batches[i]) applyToFlat(model, op);
  }
  return visibleOf(model, initial.root.id);
}
