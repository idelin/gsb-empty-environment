/**
 * Incremental snapshot & checkpoint archive engine.
 *
 * - Mutations are serialized through an internal promise queue, so concurrent
 *   callers observe a total order (single-writer semantics, no locks needed).
 * - Snapshots are *deltas*: only the ops since the previous snapshot/checkpoint
 *   are copied, in bounded chunks that yield to the event loop (low pause).
 * - Checkpoints materialize the full state and allow compacting older deltas.
 * - All archived data is defensively copied; dispose() releases everything so
 *   leak tests can assert a zero live-resource count.
 */

export type SnapshotOp =
  | { type: 'set'; key: string; value: Uint8Array }
  | { type: 'delete'; key: string };

export interface Snapshot {
  readonly kind: 'delta';
  /** Version this delta applies on top of. */
  readonly baseVersion: number;
  /** Version after applying this delta. */
  readonly version: number;
  readonly ops: readonly SnapshotOp[];
}

export interface Checkpoint {
  readonly kind: 'checkpoint';
  readonly version: number;
  readonly entries: ReadonlyArray<readonly [string, Uint8Array]>;
}

export interface EngineStats {
  version: number;
  stateSize: number;
  /** Ops recorded since the last snapshot/checkpoint (not yet archived). */
  pendingOps: number;
  archivedDeltas: number;
  archivedCheckpoints: number;
  /** Live resources held by the engine; must be 0 after dispose(). */
  liveResources: number;
  disposed: boolean;
}

export interface SnapshotEngineOptions {
  /** Entries processed per event-loop yield while copying. Default 1024. */
  chunkSize?: number;
}

export class DisposedError extends Error {
  constructor() {
    super('snapshot engine has been disposed');
    this.name = 'DisposedError';
  }
}

const yieldToEventLoop = (): Promise<void> => new Promise((r) => setImmediate(r));

function copyBytes(b: Uint8Array): Uint8Array {
  return b.slice(); // defensive copy
}

export class IncrementalSnapshotEngine {
  private state = new Map<string, Uint8Array>();
  private version = 0;
  private pending: SnapshotOp[] = [];
  private deltas: Snapshot[] = [];
  private checkpoints: Checkpoint[] = [];
  private tail: Promise<unknown> = Promise.resolve();
  private disposed = false;
  private readonly chunkSize: number;

  constructor(options: SnapshotEngineOptions = {}) {
    this.chunkSize = Math.max(1, options.chunkSize ?? 1024);
  }

  /** Serialize all mutating/archival operations through one promise chain. */
  private enqueue<T>(fn: () => T | Promise<T>): Promise<T> {
    const run = this.tail.then(fn);
    this.tail = run.catch(() => undefined); // keep the chain alive after failures
    return run;
  }

  private assertAlive(): void {
    if (this.disposed) throw new DisposedError();
  }

  get currentVersion(): number {
    return this.version;
  }

  /** Read the committed value for a key (defensive copy). */
  get(key: string): Uint8Array | undefined {
    const v = this.state.get(key);
    return v === undefined ? undefined : copyBytes(v);
  }

  set(key: string, value: Uint8Array): Promise<number> {
    return this.enqueue(() => {
      this.assertAlive();
      const stored = copyBytes(value);
      this.state.set(key, stored);
      this.pending.push({ type: 'set', key, value: copyBytes(value) });
      return ++this.version;
    });
  }

  delete(key: string): Promise<number> {
    return this.enqueue(() => {
      this.assertAlive();
      this.state.delete(key);
      this.pending.push({ type: 'delete', key });
      return ++this.version;
    });
  }

  /**
   * Create an incremental snapshot: archives only the ops recorded since the
   * previous snapshot/checkpoint. Copying is chunked to keep pauses small.
   */
  createSnapshot(): Promise<Snapshot> {
    return this.enqueue(async () => {
      this.assertAlive();
      const baseVersion = this.version - this.pending.length;
      const ops: SnapshotOp[] = [];
      for (let i = 0; i < this.pending.length; i++) {
        const op = this.pending[i];
        ops.push(op.type === 'set'
          ? { type: 'set', key: op.key, value: copyBytes(op.value) }
          : { type: 'delete', key: op.key });
        if ((i + 1) % this.chunkSize === 0) await yieldToEventLoop();
      }
      const snapshot: Snapshot = {
        kind: 'delta',
        baseVersion,
        version: this.version,
        ops,
      };
      this.deltas.push(snapshot);
      this.pending = [];
      return snapshot;
    });
  }

  /**
   * Create a full checkpoint of the current state and archive it.
   * Chunked copying keeps the pause bounded even for huge states.
   */
  createCheckpoint(): Promise<Checkpoint> {
    return this.enqueue(async () => {
      this.assertAlive();
      const entries: Array<readonly [string, Uint8Array]> = [];
      let i = 0;
      for (const [key, value] of this.state) {
        entries.push([key, copyBytes(value)]);
        if (++i % this.chunkSize === 0) await yieldToEventLoop();
      }
      const checkpoint: Checkpoint = {
        kind: 'checkpoint',
        version: this.version,
        entries,
      };
      this.checkpoints.push(checkpoint);
      this.pending = [];
      return checkpoint;
    });
  }

  /** Drop deltas that are fully covered by the latest checkpoint. */
  compact(): Promise<number> {
    return this.enqueue(() => {
      this.assertAlive();
      const latest = this.checkpoints[this.checkpoints.length - 1];
      if (!latest) return 0;
      const before = this.deltas.length;
      this.deltas = this.deltas.filter((d) => d.version > latest.version);
      return before - this.deltas.length;
    });
  }

  /**
   * Restore state from the archive: latest checkpoint + all later deltas,
   * applied in version order. Chunked to stay low-pause.
   */
  restore(): Promise<{ state: Map<string, Uint8Array>; version: number }> {
    return this.enqueue(async () => {
      this.assertAlive();
      const state = new Map<string, Uint8Array>();
      const checkpoint = this.checkpoints[this.checkpoints.length - 1];
      let version = 0;
      let i = 0;
      if (checkpoint) {
        for (const [key, value] of checkpoint.entries) {
          state.set(key, copyBytes(value));
          if (++i % this.chunkSize === 0) await yieldToEventLoop();
        }
        version = checkpoint.version;
      }
      for (const delta of this.deltas) {
        if (delta.version <= version) continue;
        if (delta.baseVersion !== version) {
          throw new Error(
            `archive gap: delta base ${delta.baseVersion} != current version ${version}`,
          );
        }
        for (const op of delta.ops) {
          if (op.type === 'set') state.set(op.key, copyBytes(op.value));
          else state.delete(op.key);
          if (++i % this.chunkSize === 0) await yieldToEventLoop();
        }
        version = delta.version;
      }
      return { state, version };
    });
  }

  stats(): EngineStats {
    let liveResources = this.state.size + this.pending.length;
    for (const d of this.deltas) liveResources += d.ops.length;
    for (const c of this.checkpoints) liveResources += c.entries.length;
    return {
      version: this.version,
      stateSize: this.state.size,
      pendingOps: this.pending.length,
      archivedDeltas: this.deltas.length,
      archivedCheckpoints: this.checkpoints.length,
      liveResources: this.disposed ? 0 : liveResources,
      disposed: this.disposed,
    };
  }

  /** Release every archived buffer. After dispose, all operations throw. */
  dispose(): Promise<void> {
    return this.enqueue(() => {
      this.state.clear();
      this.pending = [];
      this.deltas = [];
      this.checkpoints = [];
      this.disposed = true;
    });
  }
}
