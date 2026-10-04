import {
  BatchReleasedError,
  LoadCancelledError,
  StaleViewError,
  UnknownColumnError,
} from './errors';
import { Stats } from './stats';
import { Value } from './types';

/**
 * Physical column layout: a dense `values` array plus a `nulls` bitmap
 * (1 = null). Narrow columns live inline in the batch buffers; wide
 * columns are fetched on demand through a WideLoader and cached per batch.
 */
export interface ColumnData {
  values: ReadonlyArray<Value>;
  nulls: Uint8Array;
}

export type WideLoader = (ctx: { signal?: AbortSignal }) => Promise<ColumnData>;

/**
 * A physical buffer slot. Slots are reused: when a new batch is emitted
 * into an occupied slot, the old batch is released and `generation` is
 * bumped, so any view or selection vector still pointing at the old
 * contents fails its liveness check instead of silently reading new data.
 */
export interface Slot {
  generation: number;
  current: ColumnBatch | null;
}

/**
 * Read-only accessor for one column of one batch. Every access revalidates
 * that the owning batch is still alive, so a view outliving its batch
 * (released, or its slot recycled) throws instead of reading stale rows.
 */
export class ColumnView {
  constructor(
    private readonly batch: ColumnBatch,
    readonly column: string,
    private readonly data: ColumnData,
  ) {}

  get length(): number {
    return this.data.values.length;
  }

  isNull(row: number): boolean {
    this.assertValid();
    return this.data.nulls[row] === 1;
  }

  valueAt(row: number): Value {
    this.assertValid();
    return this.data.nulls[row] === 1 ? null : this.data.values[row];
  }

  private assertValid(): void {
    if (this.batch.released) {
      throw new BatchReleasedError(
        `view on column '${this.column}' of batch ${this.batch.batchId} ` +
          `(version ${this.batch.version}) used after the batch was released`,
      );
    }
    if (!this.batch.isAlive()) {
      throw new StaleViewError(
        `view on column '${this.column}' of batch ${this.batch.batchId} ` +
          `(version ${this.batch.version}) is stale: its slot was recycled`,
      );
    }
  }
}

/**
 * The result of narrow-column filtering: row numbers within one batch.
 * The vector is bound to the batch identity (batchId + data version);
 * resolve() revalidates liveness so row numbers from a previous batch
 * can never be mapped onto new data in a recycled slot.
 */
export class SelectionVector {
  readonly batchId: number;
  readonly version: number;

  constructor(
    private readonly batch: ColumnBatch,
    private readonly indices: Uint32Array,
    readonly count: number,
  ) {
    this.batchId = batch.batchId;
    this.version = batch.version;
  }

  resolve(): Uint32Array {
    if (this.batch.released) {
      throw new BatchReleasedError(
        `selection for batch ${this.batchId} (version ${this.version}) ` +
          'is invalid: the batch was released',
      );
    }
    if (!this.batch.isAlive()) {
      throw new StaleViewError(
        `selection for batch ${this.batchId} (version ${this.version}) ` +
          'is stale: its slot was recycled; refusing to map old row ' +
          'numbers onto new data',
      );
    }
    return this.indices.subarray(0, this.count);
  }
}

/**
 * One batch of columnar data.
 *
 * Lifecycle: `open` -> `released`. A batch is pinned while an operator
 * still needs it; a pinned batch cannot be recycled. Releasing detaches
 * the batch from its slot and drops cached wide-column data. Views and
 * selection vectors validate against (slot.current, slot.generation) on
 * every use.
 */
export class ColumnBatch {
  readonly rowCount: number;
  private state: 'open' | 'released' = 'open';
  private pins = 0;
  private readonly wideCache = new Map<string, ColumnView>();

  constructor(
    readonly batchId: number,
    readonly version: number,
    readonly slot: Slot,
    readonly generation: number,
    readonly rowIds: ReadonlyArray<number>,
    readonly rowVersions: ReadonlyArray<number>,
    private readonly narrow: Map<string, ColumnData>,
    private readonly wideLoaders: Map<string, WideLoader>,
    private readonly stats: Stats,
  ) {
    this.rowCount = rowIds.length;
  }

  get released(): boolean {
    return this.state === 'released';
  }

  get isPinned(): boolean {
    return this.pins > 0;
  }

  isAlive(): boolean {
    return (
      this.state === 'open' &&
      this.slot.current === this &&
      this.slot.generation === this.generation
    );
  }

  pin(): void {
    this.assertAlive('pin');
    this.pins++;
  }

  unpin(): void {
    if (this.pins > 0) this.pins--;
  }

  release(): void {
    if (this.state === 'released') return;
    this.state = 'released';
    if (this.slot.current === this) this.slot.current = null;
    this.wideCache.clear();
    this.stats.batchesReleased++;
  }

  narrowColumns(): string[] {
    return Array.from(this.narrow.keys());
  }

  wideColumns(): string[] {
    return Array.from(this.wideLoaders.keys());
  }

  hasColumn(name: string): boolean {
    return this.narrow.has(name) || this.wideLoaders.has(name);
  }

  isWide(name: string): boolean {
    return this.wideLoaders.has(name);
  }

  narrowView(name: string): ColumnView {
    this.assertAlive('read');
    const data = this.narrow.get(name);
    if (!data) {
      if (this.wideLoaders.has(name)) {
        throw new UnknownColumnError(
          `column '${name}' is a wide column; load it asynchronously with loadWide()`,
        );
      }
      throw new UnknownColumnError(`unknown narrow column '${name}'`);
    }
    return new ColumnView(this, name, data);
  }

  /**
   * Asynchronously load a wide column. Cancellable through the abort
   * signal; every attempt is counted in Stats (started/completed/
   * cancelled/failed). Successful loads are cached per batch, so a
   * column is physically read at most once per batch lifetime.
   */
  async loadWide(name: string, opts?: { signal?: AbortSignal }): Promise<ColumnView> {
    this.assertAlive('load');
    const loader = this.wideLoaders.get(name);
    if (!loader) {
      if (this.narrow.has(name)) {
        throw new UnknownColumnError(
          `column '${name}' is a narrow column; read it with narrowView()`,
        );
      }
      throw new UnknownColumnError(`unknown wide column '${name}'`);
    }
    const cached = this.wideCache.get(name);
    if (cached) return cached;

    const signal = opts && opts.signal ? opts.signal : undefined;
    const st = this.stats.loadFor(name);
    st.started++;
    if (signal && signal.aborted) {
      st.cancelled++;
      throw new LoadCancelledError(
        `load of wide column '${name}' on batch ${this.batchId} cancelled before start`,
      );
    }
    let data: ColumnData;
    try {
      data = await loader({ signal });
    } catch (err) {
      if (err instanceof LoadCancelledError || (signal && signal.aborted)) {
        st.cancelled++;
        throw err instanceof LoadCancelledError
          ? err
          : new LoadCancelledError(`load of wide column '${name}' cancelled`);
      }
      st.failed++;
      throw err;
    }
    if (signal && signal.aborted) {
      st.cancelled++;
      throw new LoadCancelledError(`load of wide column '${name}' cancelled`);
    }
    this.assertAlive('load');
    st.completed++;
    const view = new ColumnView(this, name, data);
    this.wideCache.set(name, view);
    return view;
  }

  private assertAlive(op: string): void {
    if (this.state === 'released') {
      throw new BatchReleasedError(
        `cannot ${op}: batch ${this.batchId} (version ${this.version}) was released`,
      );
    }
    if (this.slot.current !== this || this.slot.generation !== this.generation) {
      throw new StaleViewError(
        `cannot ${op}: batch ${this.batchId} (version ${this.version}) lost its slot`,
      );
    }
  }
}
