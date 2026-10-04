import { StaleSelectionError } from './errors.ts';
import type { RowBatch } from './batch.ts';

/**
 * Row numbers picked by a filter, bound to the exact batch occupant they were
 * computed from (slot + batch id + data version + buffer generation).
 *
 * resolve() refuses to hand the row numbers to any other occupant, so a
 * reused buffer can never reinterpret last batch's row numbers as positions
 * into new data.
 */
export class SelectionVector {
  readonly slotId: number;
  readonly batchId: number;
  readonly dataVersion: number;
  readonly generation: number;
  readonly indices: Uint32Array;

  private constructor(
    slotId: number,
    batchId: number,
    dataVersion: number,
    generation: number,
    indices: Uint32Array,
  ) {
    this.slotId = slotId;
    this.batchId = batchId;
    this.dataVersion = dataVersion;
    this.generation = generation;
    this.indices = indices;
  }

  static from(batch: RowBatch, indices: Uint32Array): SelectionVector {
    return new SelectionVector(
      batch.slotId,
      batch.batchId,
      batch.dataVersion,
      batch.generation,
      indices,
    );
  }

  get size(): number {
    return this.indices.length;
  }

  resolve(batch: RowBatch): Uint32Array {
    if (batch.slotId !== this.slotId || batch.batchId !== this.batchId) {
      throw new StaleSelectionError(
        `selection belongs to batch ${this.batchId} in slot ${this.slotId}, ` +
          `but was applied to batch ${batch.batchId} in slot ${batch.slotId}`,
      );
    }
    if (batch.generation !== this.generation || batch.dataVersion !== this.dataVersion) {
      throw new StaleSelectionError(
        `buffer slot ${this.slotId} was reused ` +
          `(generation ${this.generation} -> ${batch.generation}, ` +
          `data version ${this.dataVersion} -> ${batch.dataVersion}); ` +
          `refusing to map stale row numbers onto new data`,
      );
    }
    return this.indices;
  }
}
