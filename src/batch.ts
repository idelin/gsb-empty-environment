import { ColumnVector, ColumnView } from './column.ts';
import { ReleasedBatchError } from './errors.ts';

export type BatchState = 'open' | 'released';

/**
 * One batch of rows occupying a physical buffer slot.
 *
 * Identity has three parts:
 *  - slotId:      which physical buffer slot this batch occupies
 *  - batchId:     logical, monotonic batch number within the source
 *  - dataVersion: version of the data carried by this batch
 *  - generation:  how many times the slot has been reused; bumped on release
 *
 * Narrow columns ship inline with the batch; wide columns stay in storage and
 * are pulled through the source's async loader only when needed.
 */
export class RowBatch {
  readonly slotId: number;
  readonly batchId: number;
  readonly dataVersion: number;
  readonly generation: number;
  readonly rowCount: number;
  readonly rowIds: Uint32Array;
  readonly rowVersions: Uint32Array;
  private readonly narrow: Map<string, ColumnVector>;
  private readonly onRelease: (batch: RowBatch) => void;
  private stateValue: BatchState = 'open';

  constructor(
    slotId: number,
    batchId: number,
    dataVersion: number,
    generation: number,
    rowCount: number,
    rowIds: Uint32Array,
    rowVersions: Uint32Array,
    narrow: Map<string, ColumnVector>,
    onRelease: (batch: RowBatch) => void,
  ) {
    this.slotId = slotId;
    this.batchId = batchId;
    this.dataVersion = dataVersion;
    this.generation = generation;
    this.rowCount = rowCount;
    this.rowIds = rowIds;
    this.rowVersions = rowVersions;
    this.narrow = narrow;
    this.onRelease = onRelease;
  }

  get state(): BatchState {
    return this.stateValue;
  }

  hasColumn(name: string): boolean {
    return this.narrow.has(name);
  }

  columnNames(): string[] {
    return [...this.narrow.keys()];
  }

  column(name: string): ColumnView {
    this.assertAlive();
    const vector = this.narrow.get(name);
    if (!vector) {
      throw new Error(`column '${name}' is not inline in batch ${this.batchId}`);
    }
    return new ColumnView(() => this.assertAlive(), vector);
  }

  rowId(rowIndex: number): number {
    this.assertAlive();
    return this.rowIds[rowIndex];
  }

  rowVersion(rowIndex: number): number {
    this.assertAlive();
    return this.rowVersions[rowIndex];
  }

  assertAlive(): void {
    if (this.stateValue !== 'open') {
      throw new ReleasedBatchError(
        `batch ${this.batchId} (slot ${this.slotId}, generation ${this.generation}) has been released`,
      );
    }
  }

  release(): void {
    if (this.stateValue === 'released') return;
    this.stateValue = 'released';
    this.onRelease(this);
  }
}
