import type { NodeType, Snapshot, SnapshotNode } from '../src/types.ts';

type NodeSpec = [
  id: string,
  generation: number,
  type: NodeType,
  parentId: string | null,
  name: string,
  digest?: string | null,
];

export function snap(...specs: NodeSpec[]): Snapshot {
  const nodes: SnapshotNode[] = specs.map(([id, generation, type, parentId, name, digest]) => ({
    id,
    generation,
    type,
    parentId,
    name,
    digest: digest ?? null,
  }));
  return { nodes };
}

export function file(id: string, parentId: string | null, name: string, digest: string, generation = 1): NodeSpec {
  return [id, generation, 'file', parentId, name, digest];
}

export function dir(id: string, parentId: string | null, name: string, generation = 1): NodeSpec {
  return [id, generation, 'dir', parentId, name, null];
}
