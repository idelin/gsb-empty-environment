import type { SnapshotNode } from '../src/index.ts';

export function dir(id: string, parentId: string | null, name: string, generation?: number): SnapshotNode {
  return { id, type: 'dir', parentId, name, generation };
}

export function file(
  id: string,
  parentId: string | null,
  name: string,
  digest: string | null = null,
  generation?: number,
): SnapshotNode {
  return { id, type: 'file', parentId, name, digest, generation };
}
