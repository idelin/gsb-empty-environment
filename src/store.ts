// Transactional store. A commit applies a batch of ops, derives ONE
// snapshot (spans + keyboard tree + data mapping) and publishes it
// atomically to subscribers. Undo/redo replay whole versions.
import { applyOps, type HeaderOp } from './model.ts';
import { deriveSnapshot } from './derive.ts';
import type { HeaderSnapshot, HeaderState } from './types.ts';

export type SnapshotListener = (snapshot: HeaderSnapshot) => void;

export class HeaderStore {
  private states: HeaderState[];
  private snapshots: HeaderSnapshot[];
  private cursor = 0;
  private listeners = new Set<SnapshotListener>();

  constructor(initial: HeaderState) {
    this.states = [initial];
    this.snapshots = [deriveSnapshot(initial, 0)];
  }

  get version(): number {
    return this.cursor;
  }

  get snapshot(): HeaderSnapshot {
    return this.snapshots[this.cursor];
  }

  stateAt(version: number): HeaderState | undefined {
    return this.states[version];
  }

  snapshotAt(version: number): HeaderSnapshot | undefined {
    return this.snapshots[version];
  }

  subscribe(listener: SnapshotListener): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  /** Apply a batch of ops as one transaction and publish one snapshot. */
  commit(ops: HeaderOp[]): HeaderSnapshot {
    if (ops.length === 0) return this.snapshot;
    const next = applyOps(this.states[this.cursor], ops);
    this.states.length = this.cursor + 1; // drop redo tail
    this.snapshots.length = this.cursor + 1;
    this.states.push(next);
    this.cursor += 1;
    this.snapshots.push(deriveSnapshot(next, this.cursor));
    this.publish();
    return this.snapshot;
  }

  undo(): HeaderSnapshot | null {
    if (this.cursor === 0) return null;
    this.cursor -= 1;
    this.publish();
    return this.snapshot;
  }

  redo(): HeaderSnapshot | null {
    if (this.cursor >= this.states.length - 1) return null;
    this.cursor += 1;
    this.publish();
    return this.snapshot;
  }

  private publish(): void {
    const snapshot = this.snapshot;
    for (const listener of this.listeners) listener(snapshot);
  }
}
