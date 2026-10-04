import { ColumnBatch, ColumnData, Slot, WideLoader } from './batch';
import { PoolExhaustedError } from './errors';
import { Stats } from './stats';
import { Value } from './types';

export interface NarrowSpec {
  values: Value[];
  nulls?: Array<number | boolean>;
}

/**
 * Declarative description of one batch, as delivered by a data source.
 * Narrow columns are given inline; wide columns are given as async
 * loaders that the executor calls only when the column is needed.
 */
export interface BatchSpec {
  version: number;
  rowIds: number[];
  rowVersions: number[];
  narrow?: Record<string, NarrowSpec>;
  wide?: Record<string, WideLoader>;
}

export function columnData(
  values: Value[],
  nulls?: Array<number | boolean>,
): ColumnData {
  const bits = new Uint8Array(values.length);
  for (let i = 0; i < values.length; i++) {
    const marked = nulls && nulls[i] ? true : false;
    bits[i] = marked || values[i] == null ? 1 : 0;
  }
  return { values, nulls: bits };
}

/**
 * Emits batches into a fixed pool of buffer slots. When every slot is
 * occupied, the least recently used slot is recycled: its batch is
 * released and the slot generation is bumped. A pinned batch blocks
 * recycling (PoolExhaustedError), which is how the executor protects
 * batches it still needs.
 */
export class BatchSource {
  readonly stats: Stats;
  private readonly slots: Slot[] = [];
  private cursor = 0;
  private nextBatchId = 1;

  constructor(
    private readonly specs: BatchSpec[],
    slotCount: number,
    stats?: Stats,
  ) {
    if (slotCount < 1) throw new Error('slotCount must be >= 1');
    this.stats = stats || new Stats();
    for (let i = 0; i < slotCount; i++) {
      this.slots.push({ generation: 0, current: null });
    }
  }

  get slotCount(): number {
    return this.slots.length;
  }

  async *batches(): AsyncGenerator<ColumnBatch, void, undefined> {
    for (const spec of this.specs) {
      const slot = this.slots[this.cursor % this.slots.length];
      this.cursor++;
      if (slot.current) {
        if (slot.current.isPinned) {
          throw new PoolExhaustedError(
            `slot is pinned by batch ${slot.current.batchId}; increase slotCount`,
          );
        }
        slot.current.release();
        this.stats.batchesRecycled++;
      }
      slot.generation++;

      const narrow = new Map<string, ColumnData>();
      const specNarrow = spec.narrow || {};
      for (const name of Object.keys(specNarrow)) {
        narrow.set(name, columnData(specNarrow[name].values, specNarrow[name].nulls));
      }
      const wideLoaders = new Map<string, WideLoader>();
      const specWide = spec.wide || {};
      for (const name of Object.keys(specWide)) {
        wideLoaders.set(name, specWide[name]);
      }

      const batch = new ColumnBatch(
        this.nextBatchId++,
        spec.version,
        slot,
        slot.generation,
        spec.rowIds,
        spec.rowVersions,
        narrow,
        wideLoaders,
        this.stats,
      );
      slot.current = batch;
      this.stats.batchesOpened++;
      yield batch;
    }
  }
}
