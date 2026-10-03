export interface Dot {
  nodeId: string;
  clock: number;
}

export interface Entry<V = unknown> {
  key: string;
  value: V | null;
  tombstone: boolean;
  deletedAt: Dot | null;
  sizeBytes: number;
}

export type KnowledgeVector = Record<string, number>;

export interface GcStats {
  frontier: Record<string, number>;
  scanned: number;
  reclaimed: number;
  reclaimedBytes: number;
  remainingBytes: number;
}
