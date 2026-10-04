import type { Snapshot, SnapshotNode } from './types.ts';

export class SnapshotError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'SnapshotError';
  }
}

export function identityKey(id: string, generation: number): string {
  return `${id}#${generation}`;
}

export interface SnapshotIndex {
  byKey: Map<string, SnapshotNode>;
  byId: Map<string, SnapshotNode>;
  childrenOf: Map<string | null, SnapshotNode[]>;
  pathOf: Map<string, string>;
  byPath: Map<string, SnapshotNode>;
}

export function indexSnapshot(snapshot: Snapshot): SnapshotIndex {
  const byKey = new Map<string, SnapshotNode>();
  const byId = new Map<string, SnapshotNode>();
  for (const node of snapshot.nodes) {
    if (node.name.length === 0 || node.name.includes('/')) {
      throw new SnapshotError(`node ${identityKey(node.id, node.generation)} has invalid name "${node.name}"`);
    }
    const key = identityKey(node.id, node.generation);
    if (byKey.has(key)) {
      throw new SnapshotError(`duplicate node identity ${key}`);
    }
    if (byId.has(node.id)) {
      throw new SnapshotError(
        `duplicate node id "${node.id}" in one snapshot; id reuse must replace the old generation, not coexist`,
      );
    }
    byKey.set(key, node);
    byId.set(node.id, node);
  }

  for (const node of snapshot.nodes) {
    if (node.parentId === null) continue;
    const parent = byId.get(node.parentId);
    if (!parent) {
      throw new SnapshotError(
        `node ${identityKey(node.id, node.generation)} references missing parent "${node.parentId}"`,
      );
    }
    if (parent.type !== 'dir') {
      throw new SnapshotError(`parent "${node.parentId}" of node "${node.id}" is not a directory`);
    }
  }

  const childrenOf = new Map<string | null, SnapshotNode[]>();
  for (const node of snapshot.nodes) {
    const list = childrenOf.get(node.parentId);
    if (list) list.push(node);
    else childrenOf.set(node.parentId, [node]);
  }

  const pathOf = new Map<string, string>();
  const byPath = new Map<string, SnapshotNode>();
  const visiting = new Set<string>();
  const computePath = (node: SnapshotNode): string => {
    const key = identityKey(node.id, node.generation);
    const cached = pathOf.get(key);
    if (cached !== undefined) return cached;
    if (visiting.has(key)) {
      throw new SnapshotError(`cycle detected in parent chain at ${key}`);
    }
    visiting.add(key);
    const path =
      node.parentId === null ? `/${node.name}` : `${computePath(byId.get(node.parentId)!)}/${node.name}`;
    visiting.delete(key);
    pathOf.set(key, path);
    if (byPath.has(path)) {
      throw new SnapshotError(`duplicate path "${path}"`);
    }
    byPath.set(path, node);
    return path;
  };
  for (const node of snapshot.nodes) computePath(node);

  return { byKey, byId, childrenOf, pathOf, byPath };
}

export function parentIdentityKey(
  index: SnapshotIndex,
  node: SnapshotNode,
): string | null {
  if (node.parentId === null) return null;
  const parent = index.byId.get(node.parentId);
  if (!parent) {
    throw new SnapshotError(
      `node ${identityKey(node.id, node.generation)} references missing parent "${node.parentId}"`,
    );
  }
  return identityKey(parent.id, parent.generation);
}
