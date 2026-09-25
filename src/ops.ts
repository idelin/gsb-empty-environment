import type { GlyphMetrics } from './measure.ts';
import { derive } from './derive.ts';
import type { GroupNode, HeaderDoc, HeaderNode, HeaderSnapshot } from './types.ts';

export type Op =
  | { type: 'hide'; leafId: string }
  | { type: 'show'; leafId: string }
  | { type: 'move'; leafId: string; targetGroupId: string; index: number }
  | { type: 'setCollapsed'; groupId: string; collapsed: boolean };

export interface AppliedOp {
  op: Op;
  inverse: Op;
}

export function findGroup(doc: HeaderDoc, groupId: string): GroupNode {
  const stack: GroupNode[] = [doc.root];
  while (stack.length > 0) {
    const group = stack.pop()!;
    if (group.id === groupId) return group;
    for (const child of group.children) if (child.kind === 'group') stack.push(child);
  }
  throw new Error(`unknown group: ${groupId}`);
}

export function findLeafParent(doc: HeaderDoc, leafId: string): { parent: GroupNode; index: number } {
  const stack: GroupNode[] = [doc.root];
  while (stack.length > 0) {
    const group = stack.pop()!;
    for (let i = 0; i < group.children.length; i++) {
      const child: HeaderNode = group.children[i];
      if (child.kind === 'leaf' && child.id === leafId) return { parent: group, index: i };
      if (child.kind === 'group') stack.push(child);
    }
  }
  throw new Error(`unknown leaf: ${leafId}`);
}

/** Apply one op to the document and record its exact inverse. */
export function applyOp(doc: HeaderDoc, op: Op): AppliedOp {
  switch (op.type) {
    case 'hide': {
      findLeafParent(doc, op.leafId);
      const wasHidden = doc.hidden.has(op.leafId);
      doc.hidden.add(op.leafId);
      const inverse: Op = wasHidden
        ? { type: 'hide', leafId: op.leafId }
        : { type: 'show', leafId: op.leafId };
      return { op, inverse };
    }
    case 'show': {
      findLeafParent(doc, op.leafId);
      const wasHidden = doc.hidden.has(op.leafId);
      doc.hidden.delete(op.leafId);
      const inverse: Op = wasHidden
        ? { type: 'hide', leafId: op.leafId }
        : { type: 'show', leafId: op.leafId };
      return { op, inverse };
    }
    case 'setCollapsed': {
      findGroup(doc, op.groupId);
      if (op.groupId === doc.root.id) throw new Error('cannot collapse the root group');
      const wasCollapsed = doc.collapsed.has(op.groupId);
      if (op.collapsed) doc.collapsed.add(op.groupId);
      else doc.collapsed.delete(op.groupId);
      const inverse: Op = { type: 'setCollapsed', groupId: op.groupId, collapsed: wasCollapsed };
      return { op, inverse };
    }
    case 'move': {
      const { parent, index } = findLeafParent(doc, op.leafId);
      const target = findGroup(doc, op.targetGroupId);
      const [leaf] = parent.children.splice(index, 1);
      const clamped = Math.max(0, Math.min(op.index, target.children.length));
      target.children.splice(clamped, 0, leaf);
      const inverse: Op = { type: 'move', leafId: op.leafId, targetGroupId: parent.id, index };
      return { op, inverse };
    }
  }
}

/**
 * A store commits transactions: all ops in a transaction are applied to the
 * document first, then exactly one snapshot (spans + keyboard tree + data
 * mapping) is derived and published atomically.
 */
export class HeaderStore {
  doc: HeaderDoc;
  metrics: GlyphMetrics;
  version: number;
  snapshot: HeaderSnapshot;
  private history: AppliedOp[][];

  constructor(doc: HeaderDoc, metrics: GlyphMetrics) {
    this.doc = doc;
    this.metrics = metrics;
    this.version = 0;
    this.snapshot = derive(doc, 0, metrics);
    this.history = [];
  }

  commit(ops: Op[]): HeaderSnapshot {
    const applied: AppliedOp[] = [];
    for (const op of ops) applied.push(applyOp(this.doc, op));
    this.version += 1;
    this.snapshot = derive(this.doc, this.version, this.metrics);
    this.history.push(applied);
    return this.snapshot;
  }

  undo(): HeaderSnapshot {
    const applied = this.history.pop();
    if (applied === undefined) return this.snapshot;
    for (let i = applied.length - 1; i >= 0; i--) applyOp(this.doc, applied[i].inverse);
    this.version += 1;
    this.snapshot = derive(this.doc, this.version, this.metrics);
    return this.snapshot;
  }
}
