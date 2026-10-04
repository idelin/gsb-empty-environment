import type { Plan, PlanOp, Snapshot, SnapshotNode } from './types.ts';
import { identityKey, indexSnapshot } from './snapshot.ts';

export class ApplyError extends Error {
  readonly opIndex: number;
  constructor(message: string, opIndex: number = -1) {
    super(message);
    this.name = 'ApplyError';
    this.opIndex = opIndex;
  }
}

interface TreeState {
  byKey: Map<string, SnapshotNode>;
  keyById: Map<string, string>;
  tempPathOf: Map<string, string>;
}

function describeOp(op: PlanOp): string {
  switch (op.op) {
    case 'create':
      return `create ${identityKey(op.node.id, op.node.generation)}`;
    case 'delete':
      return `delete ${identityKey(op.id, op.generation)}`;
    case 'move':
      return `move ${identityKey(op.id, op.generation)} -> parent=${op.toParentId ?? '<root>'} name=${op.toName}`;
    case 'moveToTemp':
      return `moveToTemp ${identityKey(op.id, op.generation)} -> ${op.tempPath}`;
    case 'updateContent':
      return `updateContent ${identityKey(op.id, op.generation)}`;
  }
}

export function diffTrees(actual: Snapshot, expected: Snapshot): string[] {
  const mismatches: string[] = [];
  const actualIndex = indexSnapshot(actual);
  const expectedIndex = indexSnapshot(expected);

  const parentKeyOf = (
    index: Map<string, SnapshotNode>,
    node: SnapshotNode,
  ): string | null => {
    if (node.parentId === null) return null;
    const parent = index.get(node.parentId);
    return parent ? identityKey(parent.id, parent.generation) : `<missing:${node.parentId}>`;
  };

  for (const [key, expectedNode] of expectedIndex.byKey) {
    const actualNode = actualIndex.byKey.get(key);
    if (!actualNode) {
      mismatches.push(`missing node ${key}`);
      continue;
    }
    if (actualNode.type !== expectedNode.type) {
      mismatches.push(`node ${key} type: expected ${expectedNode.type}, got ${actualNode.type}`);
    }
    if (actualNode.name !== expectedNode.name) {
      mismatches.push(`node ${key} name: expected "${expectedNode.name}", got "${actualNode.name}"`);
    }
    if (actualNode.digest !== expectedNode.digest) {
      mismatches.push(`node ${key} digest: expected ${expectedNode.digest}, got ${actualNode.digest}`);
    }
    const actualParent = parentKeyOf(actualIndex.byId, actualNode);
    const expectedParent = parentKeyOf(expectedIndex.byId, expectedNode);
    if (actualParent !== expectedParent) {
      mismatches.push(`node ${key} parent: expected ${expectedParent ?? '<root>'}, got ${actualParent ?? '<root>'}`);
    }
  }
  for (const key of actualIndex.byKey.keys()) {
    if (!expectedIndex.byKey.has(key)) {
      mismatches.push(`unexpected node ${key}`);
    }
  }
  return mismatches;
}

export function applyPlan(oldSnap: Snapshot, plan: Plan | PlanOp[], expectedNewSnap: Snapshot): Snapshot {
  const ops: PlanOp[] = Array.isArray(plan) ? plan : plan.ops;
  indexSnapshot(oldSnap);
  indexSnapshot(expectedNewSnap);

  const state: TreeState = {
    byKey: new Map(),
    keyById: new Map(),
    tempPathOf: new Map(),
  };
  for (const node of oldSnap.nodes) {
    const key = identityKey(node.id, node.generation);
    state.byKey.set(key, { ...node });
    state.keyById.set(node.id, key);
  }

  const pathOfKey = (key: string): string => {
    const temp = state.tempPathOf.get(key);
    if (temp !== undefined) return temp;
    const node = state.byKey.get(key)!;
    if (node.parentId === null) return `/${node.name}`;
    const parentKey = state.keyById.get(node.parentId);
    if (parentKey === undefined) {
      throw new ApplyError(`node ${key} references missing parent "${node.parentId}"`);
    }
    return `${pathOfKey(parentKey)}/${node.name}`;
  };

  const requireNode = (id: string, generation: number, opIndex: number): SnapshotNode => {
    const key = identityKey(id, generation);
    const node = state.byKey.get(key);
    if (!node) {
      throw new ApplyError(`op #${opIndex} (${describeOp(ops[opIndex])}): node ${key} does not exist`, opIndex);
    }
    return node;
  };

  const resolveParentKey = (parentId: string | null, opIndex: number): string | null => {
    if (parentId === null) return null;
    const parentKey = state.keyById.get(parentId);
    if (parentKey === undefined) {
      throw new ApplyError(
        `op #${opIndex} (${describeOp(ops[opIndex])}): parent "${parentId}" does not exist`,
        opIndex,
      );
    }
    const parent = state.byKey.get(parentKey)!;
    if (parent.type !== 'dir') {
      throw new ApplyError(
        `op #${opIndex} (${describeOp(ops[opIndex])}): parent "${parentId}" is not a directory`,
        opIndex,
      );
    }
    return parentKey;
  };

  const assertPathFree = (path: string, selfKey: string | null, opIndex: number): void => {
    for (const key of state.byKey.keys()) {
      if (key === selfKey) continue;
      if (pathOfKey(key) === path) {
        throw new ApplyError(
          `op #${opIndex} (${describeOp(ops[opIndex])}): path "${path}" is already occupied by ${key}`,
          opIndex,
        );
      }
    }
  };

  for (let opIndex = 0; opIndex < ops.length; opIndex += 1) {
    const op = ops[opIndex];
    switch (op.op) {
      case 'create': {
        const key = identityKey(op.node.id, op.node.generation);
        if (state.byKey.has(key) || state.keyById.has(op.node.id)) {
          throw new ApplyError(`op #${opIndex} (${describeOp(op)}): node id "${op.node.id}" already exists`, opIndex);
        }
        const parentKey = resolveParentKey(op.node.parentId, opIndex);
        const parentPath = parentKey === null ? '' : pathOfKey(parentKey);
        const destPath = `${parentPath}/${op.node.name}`;
        assertPathFree(destPath, null, opIndex);
        state.byKey.set(key, { ...op.node });
        state.keyById.set(op.node.id, key);
        break;
      }
      case 'delete': {
        const key = identityKey(op.id, op.generation);
        const node = requireNode(op.id, op.generation, opIndex);
        for (const other of state.byKey.values()) {
          if (other.parentId === node.id) {
            const otherKey = identityKey(other.id, other.generation);
            if (state.tempPathOf.has(otherKey)) continue;
            throw new ApplyError(
              `op #${opIndex} (${describeOp(op)}): directory still contains ${otherKey}`,
              opIndex,
            );
          }
        }
        state.byKey.delete(key);
        state.keyById.delete(node.id);
        state.tempPathOf.delete(key);
        break;
      }
      case 'move': {
        const key = identityKey(op.id, op.generation);
        const node = requireNode(op.id, op.generation, opIndex);
        const parentKey = resolveParentKey(op.toParentId, opIndex);
        const parentPath = parentKey === null ? '' : pathOfKey(parentKey);
        const destPath = `${parentPath}/${op.toName}`;
        assertPathFree(destPath, key, opIndex);
        node.parentId = op.toParentId;
        node.name = op.toName;
        state.tempPathOf.delete(key);
        break;
      }
      case 'moveToTemp': {
        const key = identityKey(op.id, op.generation);
        requireNode(op.id, op.generation, opIndex);
        assertPathFree(op.tempPath, key, opIndex);
        state.tempPathOf.set(key, op.tempPath);
        break;
      }
      case 'updateContent': {
        const node = requireNode(op.id, op.generation, opIndex);
        node.digest = op.digest;
        break;
      }
    }
  }

  if (state.tempPathOf.size > 0) {
    const leftovers = [...state.tempPathOf.entries()].map(([key, path]) => `${key} at ${path}`);
    throw new ApplyError(`plan leaves ${leftovers.length} node(s) at temporary paths: ${leftovers.join(', ')}`);
  }

  const result: Snapshot = { nodes: [...state.byKey.values()].map((node) => ({ ...node })) };
  const mismatches = diffTrees(result, expectedNewSnap);
  if (mismatches.length > 0) {
    throw new ApplyError(
      `resulting tree does not match the target snapshot:\n${mismatches.map((m) => `  - ${m}`).join('\n')}`,
    );
  }
  return result;
}
