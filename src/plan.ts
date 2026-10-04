import type { Plan, PlanOp, Snapshot, SnapshotNode } from './types.ts';
import { identityKey, indexSnapshot, parentIdentityKey } from './snapshot.ts';
import { diffSnapshots } from './diff.ts';

export class PlanError extends Error {
  readonly conflicts: string[];
  constructor(message: string, conflicts: string[] = []) {
    super(conflicts.length > 0 ? `${message}:\n${conflicts.map((c) => `  - ${c}`).join('\n')}` : message);
    this.name = 'PlanError';
    this.conflicts = conflicts;
  }
}

interface Cap {
  op: number;
}

interface MoveInfo {
  key: string;
  node: SnapshotNode;
  opIndex: number;
  vacated: Cap;
  placed: Cap;
  split: boolean;
}

interface Edge {
  from: Cap;
  to: number;
}

export function buildPlan(oldSnap: Snapshot, newSnap: Snapshot): Plan {
  const oldIndex = indexSnapshot(oldSnap);
  const newIndex = indexSnapshot(newSnap);
  const changes = diffSnapshots(oldSnap, newSnap);

  const ops: PlanOp[] = [];
  const edges: Edge[] = [];
  const conflicts: string[] = [];

  const moveInfo = new Map<string, MoveInfo>();
  const createPlaced = new Map<string, Cap>();
  const deleteDone = new Map<string, Cap>();

  const isDeleted = (node: SnapshotNode): boolean => {
    const next = newIndex.byKey.get(identityKey(node.id, node.generation));
    return !next || next.type !== node.type;
  };
  const isCreated = (node: SnapshotNode): boolean => {
    const prev = oldIndex.byKey.get(identityKey(node.id, node.generation));
    return !prev || prev.type !== node.type;
  };

  for (const node of oldSnap.nodes) {
    if (!isDeleted(node)) continue;
    const key = identityKey(node.id, node.generation);
    deleteDone.set(key, { op: ops.length });
    ops.push({ op: 'delete', id: node.id, generation: node.generation });
  }

  for (const node of newSnap.nodes) {
    if (!isCreated(node)) continue;
    const key = identityKey(node.id, node.generation);
    createPlaced.set(key, { op: ops.length });
    ops.push({ op: 'create', node: { ...node } });
  }

  for (const node of newSnap.nodes) {
    const key = identityKey(node.id, node.generation);
    const prev = oldIndex.byKey.get(key);
    if (!prev || prev.type !== node.type) continue;
    const parentChanged = parentIdentityKey(oldIndex, prev) !== parentIdentityKey(newIndex, node);
    const nameChanged = prev.name !== node.name;
    if (parentChanged || nameChanged) {
      const info: MoveInfo = {
        key,
        node,
        opIndex: ops.length,
        vacated: { op: ops.length },
        placed: { op: ops.length },
        split: false,
      };
      moveInfo.set(key, info);
      ops.push({
        op: 'move',
        id: node.id,
        generation: node.generation,
        toParentId: node.parentId,
        toName: node.name,
      });
    }
    if (prev.digest !== node.digest) {
      ops.push({ op: 'updateContent', id: node.id, generation: node.generation, digest: node.digest });
    }
  }

  const placedCapOf = (key: string): Cap | null => {
    const info = moveInfo.get(key);
    if (info) return info.placed;
    const created = createPlaced.get(key);
    if (created) return created;
    return null;
  };

  const vacatedCapOf = (key: string): Cap | null => {
    const info = moveInfo.get(key);
    if (info) return info.vacated;
    const deleted = deleteDone.get(key);
    if (deleted) return deleted;
    return null;
  };

  const addParentPlacementDep = (node: SnapshotNode, opIndex: number): void => {
    if (node.parentId === null) return;
    const parent = newIndex.byId.get(node.parentId)!;
    const parentKey = identityKey(parent.id, parent.generation);
    if (deleteDone.has(parentKey)) {
      conflicts.push(
        `node ${identityKey(node.id, node.generation)} is placed under parent ${parentKey} which is deleted; resolve the deleted parent first`,
      );
      return;
    }
    const cap = placedCapOf(parentKey);
    if (cap) edges.push({ from: cap, to: opIndex });
  };

  const addDestinationOccupantDep = (node: SnapshotNode, opIndex: number): void => {
    const key = identityKey(node.id, node.generation);
    const destPath = newIndex.pathOf.get(key)!;
    const occupant = oldIndex.byPath.get(destPath);
    if (!occupant) return;
    const occupantKey = identityKey(occupant.id, occupant.generation);
    if (occupantKey === key) return;
    const cap = vacatedCapOf(occupantKey);
    if (cap) {
      edges.push({ from: cap, to: opIndex });
    } else {
      conflicts.push(
        `destination path "${destPath}" of ${key} is occupied by ${occupantKey}, which neither moves nor is deleted`,
      );
    }
  };

  for (const info of moveInfo.values()) {
    addParentPlacementDep(info.node, info.opIndex);
    addDestinationOccupantDep(info.node, info.opIndex);
  }

  for (const [key, cap] of createPlaced) {
    const node = newIndex.byKey.get(key)!;
    addParentPlacementDep(node, cap.op);
    addDestinationOccupantDep(node, cap.op);
  }

  for (const [key, cap] of deleteDone) {
    const node = oldIndex.byKey.get(key)!;
    const children = oldIndex.childrenOf.get(node.id) ?? [];
    for (const child of children) {
      const childKey = identityKey(child.id, child.generation);
      const childVacated = vacatedCapOf(childKey);
      if (childVacated) {
        edges.push({ from: childVacated, to: cap.op });
      } else {
        conflicts.push(
          `directory ${key} is deleted but child ${childKey} survives without moving away; resolve the child first`,
        );
      }
    }
  }

  if (conflicts.length > 0) {
    throw new PlanError('cannot build a consistent plan', conflicts);
  }

  const usedPaths = new Set<string>([...oldIndex.byPath.keys(), ...newIndex.byPath.keys()]);
  let tempCounter = 0;
  const allocTempPath = (): string => {
    let path: string;
    do {
      tempCounter += 1;
      path = `/.snapdiff-tmp/${tempCounter}`;
    } while (usedPaths.has(path));
    usedPaths.add(path);
    return path;
  };

  const topoOrder = (): { order: number[]; remaining: Set<number> } => {
    const indegree = new Array<number>(ops.length).fill(0);
    const adjacent: number[][] = ops.map(() => []);
    const seen = new Set<string>();
    for (const edge of edges) {
      const from = edge.from.op;
      const to = edge.to;
      if (from === to) continue;
      const dedupeKey = `${from}>${to}`;
      if (seen.has(dedupeKey)) continue;
      seen.add(dedupeKey);
      adjacent[from].push(to);
      indegree[to] += 1;
    }
    const ready: number[] = [];
    for (let i = 0; i < ops.length; i += 1) {
      if (indegree[i] === 0) ready.push(i);
    }
    const order: number[] = [];
    const emitted = new Array<boolean>(ops.length).fill(false);
    while (ready.length > 0) {
      ready.sort((a, b) => a - b);
      const current = ready.shift()!;
      if (emitted[current]) continue;
      emitted[current] = true;
      order.push(current);
      for (const next of adjacent[current]) {
        indegree[next] -= 1;
        if (indegree[next] === 0) ready.push(next);
      }
    }
    const remaining = new Set<number>();
    for (let i = 0; i < ops.length; i += 1) {
      if (!emitted[i]) remaining.add(i);
    }
    return { order, remaining };
  };

  let result = topoOrder();
  while (result.remaining.size > 0) {
    const candidate = [...moveInfo.values()].find(
      (info) => !info.split && result.remaining.has(info.opIndex),
    );
    if (!candidate) {
      throw new PlanError('unresolvable dependency cycle that no temporary move can break');
    }
    candidate.split = true;
    const tempPath = allocTempPath();
    candidate.vacated.op = ops.length;
    ops.push({
      op: 'moveToTemp',
      id: candidate.node.id,
      generation: candidate.node.generation,
      tempPath,
    });
    result = topoOrder();
  }

  return { changes, ops: result.order.map((index) => ops[index]) };
}
