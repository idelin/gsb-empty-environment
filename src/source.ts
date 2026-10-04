import { RowBatch } from './batch.ts';
import { ColumnVector } from './column.ts';
import { QueryAbortedError, throwIfAborted } from './errors.ts';
import type { Value } from './types.ts';

export interface BatchSpec {
  dataVersion: number;
  rowIds: number[];
  rowVersions: number[];
  columns: Record<string, Value[]>;
}

export interface SourceSpec {
  narrowColumns: string[];
  wideColumns: string[];
  batches: BatchSpec[];
}

export type SourceEvent =
  | { kind: 'acquire'; slotId: number; batchId: number; generation: number }
  | { kind: 'release'; slotId: number; batchId: number }
  | { kind: 'load'; column: string; batchId: number; rows: number };

export interface SourceOptions {
  poolSize?: number;
  loadLatencyMs?: number;
  onEvent?: (event: SourceEvent) => void;
}

/** Per-column asynchronous load counters. */
export class LoadStats {
  private calls = new Map<string, number>();
  private rows = new Map<string, number>();

  record(column: string, rowCount: number): void {
    this.calls.set(column, (this.calls.get(column) ?? 0) + 1);
    this.rows.set(column, (this.rows.get(column) ?? 0) + rowCount);
  }

  loadCount(column: string): number {
    return this.calls.get(column) ?? 0;
  }

  rowsLoaded(column: string): number {
    return this.rows.get(column) ?? 0;
  }

  snapshot(): Record<string, { calls: number; rowsLoaded: number }> {
    const out: Record<string, { calls: number; rowsLoaded: number }> = {};
    for (const [column, calls] of this.calls) {
      out[column] = { calls, rowsLoaded: this.rows.get(column) ?? 0 };
    }
    return out;
  }
}

interface Slot {
  id: number;
  generation: number;
}

/**
 * Serves row batches through a fixed pool of reusable buffer slots.
 * Narrow columns arrive inline with the batch; wide columns stay in the
 * backing store and are fetched asynchronously (and cancellably) on demand.
 */
export class BatchSource {
  readonly stats = new LoadStats();

  readonly spec: SourceSpec;
  private readonly poolSize: number;
  private readonly loadLatencyMs: number;
  private readonly onEvent?: (event: SourceEvent) => void;
  private readonly freeSlots: Slot[] = [];
  private readonly waiters: Array<(slot: Slot) => void> = [];
  private cursor = 0;

  constructor(spec: SourceSpec, options: SourceOptions = {}) {
    this.spec = spec;
    this.poolSize = options.poolSize ?? 2;
    this.loadLatencyMs = options.loadLatencyMs ?? 0;
    this.onEvent = options.onEvent;
    for (let id = 0; id < this.poolSize; id++) {
      this.freeSlots.push({ id, generation: 0 });
    }
  }

  /** Next batch in scan order, or null when the input is exhausted. */
  async acquire(): Promise<RowBatch | null> {
    if (this.cursor >= this.spec.batches.length) return null;
    const index = this.cursor++;
    return this.openBatch(index);
  }

  /** Re-open a previously scanned batch from storage (used by projection). */
  async fetch(batchId: number): Promise<RowBatch> {
    if (batchId < 0 || batchId >= this.spec.batches.length) {
      throw new Error(`unknown batch id ${batchId}`);
    }
    return this.openBatch(batchId);
  }

  /**
   * Asynchronously load a wide column for the given batch, optionally
   * gathered down to `indices`. Cancellable via `signal`; the batch is
   * re-validated after the async gap so a released batch can never serve data.
   */
  async loadColumn(
    batch: RowBatch,
    column: string,
    indices: Uint32Array | null,
    signal?: AbortSignal,
  ): Promise<ColumnVector> {
    batch.assertAlive();
    if (!this.spec.wideColumns.includes(column)) {
      throw new Error(`column '${column}' is not a wide column of this source`);
    }
    const token = {
      batchId: batch.batchId,
      dataVersion: batch.dataVersion,
      generation: batch.generation,
    };
    throwIfAborted(signal);
    await this.delay(signal);
    // Re-validate the token after the async gap.
    batch.assertAlive();
    if (
      batch.batchId !== token.batchId ||
      batch.dataVersion !== token.dataVersion ||
      batch.generation !== token.generation
    ) {
      throw new QueryAbortedError('batch token changed while loading');
    }
    const stored = this.spec.batches[token.batchId].columns[column];
    const picked = indices ?? Uint32Array.from({ length: stored.length }, (_, i) => i);
    const values: Value[] = new Array(picked.length);
    for (let p = 0; p < picked.length; p++) values[p] = stored[picked[p]];
    this.stats.record(column, picked.length);
    this.emit({ kind: 'load', column, batchId: token.batchId, rows: picked.length });
    return ColumnVector.fromValues(column, values);
  }

  private async openBatch(index: number): Promise<RowBatch> {
    const batchSpec = this.spec.batches[index];
    const slot = await this.acquireSlot();
    const narrow = new Map<string, ColumnVector>();
    for (const name of this.spec.narrowColumns) {
      narrow.set(name, ColumnVector.fromValues(name, batchSpec.columns[name]));
    }
    const batch = new RowBatch(
      slot.id,
      index,
      batchSpec.dataVersion,
      slot.generation,
      batchSpec.rowIds.length,
      Uint32Array.from(batchSpec.rowIds),
      Uint32Array.from(batchSpec.rowVersions),
      narrow,
      () => {
        this.emit({ kind: 'release', slotId: slot.id, batchId: index });
        this.releaseSlot(slot);
      },
    );
    this.emit({ kind: 'acquire', slotId: slot.id, batchId: index, generation: slot.generation });
    return batch;
  }

  private acquireSlot(): Promise<Slot> {
    const slot = this.freeSlots.pop();
    if (slot) return Promise.resolve(slot);
    return new Promise((resolve) => this.waiters.push(resolve));
  }

  private releaseSlot(slot: Slot): void {
    slot.generation++;
    const waiter = this.waiters.shift();
    if (waiter) waiter(slot);
    else this.freeSlots.push(slot);
  }

  private emit(event: SourceEvent): void {
    this.onEvent?.(event);
  }

  private delay(signal?: AbortSignal): Promise<void> {
    return new Promise((resolve, reject) => {
      if (signal?.aborted) {
        reject(new QueryAbortedError());
        return;
      }
      const timer = setTimeout(() => {
        signal?.removeEventListener('abort', onAbort);
        resolve();
      }, this.loadLatencyMs);
      const onAbort = () => {
        clearTimeout(timer);
        reject(new QueryAbortedError());
      };
      signal?.addEventListener('abort', onAbort, { once: true });
    });
  }
}
