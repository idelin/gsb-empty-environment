import type { Change, Snapshot, SnapshotNode } from './types.ts';
import { identityKey, indexSnapshot, parentIdentityKey } from './snapshot.ts';

function sameNode(prev: SnapshotNode, next: SnapshotNode): boolean {
  return prev.type === next.type;
}

export function diffSnapshots(oldSnap: Snapshot, newSnap: Snapshot): Change[] {
  const oldIndex = indexSnapshot(oldSnap);
  const newIndex = indexSnapshot(newSnap);
  const changes: Change[] = [];

  for (const node of newSnap.nodes) {
    const key = identityKey(node.id, node.generation);
    const prev = oldIndex.byKey.get(key);
    if (!prev || !sameNode(prev, node)) {
      changes.push({ kind: 'added', node });
      continue;
    }
    if (prev.name !== node.name) {
      changes.push({
        kind: 'renamed',
        id: node.id,
        generation: node.generation,
        oldName: prev.name,
        newName: node.name,
      });
    }
    const oldParentKey = parentIdentityKey(oldIndex, prev);
    const newParentKey = parentIdentityKey(newIndex, node);
    if (oldParentKey !== newParentKey) {
      changes.push({
        kind: 'moved',
        id: node.id,
        generation: node.generation,
        oldParent:
          prev.parentId === null
            ? null
            : { id: prev.parentId, generation: oldIndex.byId.get(prev.parentId)!.generation },
        newParent:
          node.parentId === null
            ? null
            : { id: node.parentId, generation: newIndex.byId.get(node.parentId)!.generation },
      });
    }
    if (prev.digest !== node.digest) {
      changes.push({
        kind: 'content-changed',
        id: node.id,
        generation: node.generation,
        oldDigest: prev.digest,
        newDigest: node.digest,
      });
    }
  }

  for (const node of oldSnap.nodes) {
    const key = identityKey(node.id, node.generation);
    const next = newIndex.byKey.get(key);
    if (!next || !sameNode(node, next)) {
      changes.push({ kind: 'deleted', node });
    }
  }

  return changes;
}
