import { CheckpointStore, Storage, ValidatedRow } from "./types";

export interface Deferred<T> {
  promise: Promise<T>;
  resolve(value: T): void;
  reject(error: unknown): void;
}

export function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** A one-shot async gate: callers await `wait()` until `open()` is called. */
export class Gate {
  private d = deferred<void>();
  private opened = false;

  wait(): Promise<void> {
    return this.opened ? Promise.resolve() : this.d.promise;
  }

  open(): void {
    if (!this.opened) {
      this.opened = true;
      this.d.resolve();
    }
  }

  get isOpen(): boolean {
    return this.opened;
  }
}

export interface WriteRecord {
  batchId: string;
  rows: ValidatedRow[];
}

/**
 * In-memory Storage. writeBatch is idempotent per batchId (repeat calls with
 * the same id are no-ops), mirroring the contract required by the importer.
 * `beforeWrite` lets tests inject controllable async boundaries (gates).
 */
export class MemoryStorage implements Storage {
  private batches = new Map<string, ValidatedRow[]>();
  /** Every writeBatch attempt, including idempotent no-op repeats. */
  readonly attempts: WriteRecord[] = [];
  beforeWrite?: (batchId: string, rows: ValidatedRow[]) => Promise<void>;

  async writeBatch(batchId: string, rows: ValidatedRow[]): Promise<void> {
    if (this.beforeWrite) {
      await this.beforeWrite(batchId, rows);
    }
    this.attempts.push({ batchId, rows });
    if (!this.batches.has(batchId)) {
      this.batches.set(batchId, rows.slice());
    }
  }

  committedBatchIds(): string[] {
    return Array.from(this.batches.keys());
  }

  rowsOf(batchId: string): ValidatedRow[] {
    const rows = this.batches.get(batchId);
    if (!rows) {
      throw new Error(`unknown batchId: ${batchId}`);
    }
    return rows;
  }

  totalRows(): number {
    let n = 0;
    for (const rows of this.batches.values()) {
      n += rows.length;
    }
    return n;
  }
}

/**
 * In-memory CheckpointStore. `snapshot()`/`restore()` simulate process
 * restart with a durable checkpoint log.
 */
export class MemoryCheckpointStore implements CheckpointStore {
  private committed = new Map<string, Set<string>>();

  async loadCommittedBatchIds(importId: string): Promise<Set<string>> {
    return new Set(this.committed.get(importId) || []);
  }

  async markBatchCommitted(importId: string, batchId: string): Promise<void> {
    let set = this.committed.get(importId);
    if (!set) {
      set = new Set();
      this.committed.set(importId, set);
    }
    set.add(batchId);
  }

  snapshot(): string {
    const obj: { [importId: string]: string[] } = {};
    for (const [k, v] of this.committed) {
      obj[k] = Array.from(v);
    }
    return JSON.stringify(obj);
  }

  static restore(data: string): MemoryCheckpointStore {
    const store = new MemoryCheckpointStore();
    const obj = JSON.parse(data) as { [importId: string]: string[] };
    for (const k of Object.keys(obj)) {
      store.committed.set(k, new Set(obj[k]));
    }
    return store;
  }
}
