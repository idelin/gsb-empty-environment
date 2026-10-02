export type NodeStatus = "UP" | "DOWN";

/** A single membership-state record, versioned for last-writer-wins merge. */
export interface StateRecord {
  nodeId: string;
  status: NodeStatus;
  incarnation: number;
  updatedAt: number;
}

/** Returns > 0 when `a` is strictly newer than `b`. */
export function compareRecords(a: StateRecord, b: StateRecord): number {
  if (a.incarnation !== b.incarnation) return a.incarnation - b.incarnation;
  if (a.updatedAt !== b.updatedAt) return a.updatedAt - b.updatedAt;
  if (a.status === b.status) return 0;
  return a.status === "DOWN" ? 1 : -1;
}

/** Local copy of the cluster membership table. */
export class StateTable {
  private records = new Map<string, StateRecord>();

  /** Last-writer-wins merge. Returns true when the record was applied. */
  apply(record: StateRecord): boolean {
    const current = this.records.get(record.nodeId);
    if (current && compareRecords(current, record) >= 0) return false;
    this.records.set(record.nodeId, { ...record });
    return true;
  }

  get(nodeId: string): StateRecord | undefined {
    const found = this.records.get(nodeId);
    return found ? { ...found } : undefined;
  }

  has(nodeId: string): boolean {
    return this.records.has(nodeId);
  }

  size(): number {
    return this.records.size;
  }

  /** All records, sorted by nodeId for deterministic serialization. */
  all(): StateRecord[] {
    return [...this.records.values()]
      .map((record) => ({ ...record }))
      .sort((a, b) => (a.nodeId < b.nodeId ? -1 : a.nodeId > b.nodeId ? 1 : 0));
  }
}
