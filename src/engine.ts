/**
 * 简化的向量化执行引擎：按批（batch）扫描行存宽表，
 * 每个批上进行多趟（multi-pass）谓词/聚合计算。
 *
 * 单批数据若大于 L1 容量，多趟扫描会反复驱逐缓存行（颠簸）；
 * 调节器通过观察每批的 L1 命中率动态调整批大小。
 */

import { CacheHierarchy } from "./cache.ts";
import type { CacheSnapshot } from "./cache.ts";
import { AdaptiveBatchSizeRegulator } from "./regulator.ts";

export interface WideTableOptions {
  rowCount: number;
  columnCount: number;
  bytesPerColumn?: number;
  /** 行在模拟地址空间中的起始地址。 */
  baseAddress?: number;
}

/** 行存宽表：第 r 行第 c 列位于 data[r * columnCount + c]。 */
export class WideTable {
  readonly rowCount: number;
  readonly columnCount: number;
  readonly bytesPerColumn: number;
  readonly rowSizeBytes: number;
  readonly baseAddress: number;
  readonly data: Float64Array;

  constructor(options: WideTableOptions) {
    this.rowCount = options.rowCount;
    this.columnCount = options.columnCount;
    this.bytesPerColumn = options.bytesPerColumn ?? 8;
    this.rowSizeBytes = this.columnCount * this.bytesPerColumn;
    this.baseAddress = options.baseAddress ?? 0;
    this.data = new Float64Array(this.rowCount * this.columnCount);
    for (let i = 0; i < this.data.length; i++) {
      this.data[i] = (i % 997) * 0.5;
    }
  }

  rowAddress(row: number): number {
    return this.baseAddress + row * this.rowSizeBytes;
  }

  rowSum(row: number): number {
    let sum = 0;
    const base = row * this.columnCount;
    for (let c = 0; c < this.columnCount; c++) sum += this.data[base + c];
    return sum;
  }
}

export interface BatchTrace {
  batchIndex: number;
  startRow: number;
  rows: number;
  batchSize: number;
  footprintBytes: number;
  fitsInL1: boolean;
  l1HitRate: number;
  l2HitRate: number;
  memoryReads: number;
}

export interface QueryResult {
  checksum: number;
  trace: BatchTrace[];
  stats: CacheSnapshot;
  estimatedCycles: number;
  finalBatchSize: number;
}

export interface QueryOptions {
  /** 每个批上的扫描趟数（如多谓词 + 聚合）。 */
  passes?: number;
  /** 为 true 时禁用调节器，固定使用初始批大小（对照组）。 */
  fixedBatchSize?: boolean;
}

export class VectorizedEngine {
  readonly cache: CacheHierarchy;
  readonly regulator: AdaptiveBatchSizeRegulator;

  constructor(
    cache: CacheHierarchy = new CacheHierarchy(),
    regulator?: AdaptiveBatchSizeRegulator,
  ) {
    this.cache = cache;
    this.regulator =
      regulator ??
      new AdaptiveBatchSizeRegulator({
        l1CapacityBytes: cache.l1CapacityBytes,
      });
  }

  runQuery(table: WideTable, options: QueryOptions = {}): QueryResult {
    const passes = options.passes ?? 4;
    const fixed = options.fixedBatchSize ?? false;
    this.regulator.setRowSize(table.rowSizeBytes);

    const trace: BatchTrace[] = [];
    let checksum = 0;
    let row = 0;
    let batchIndex = 0;

    while (row < table.rowCount) {
      const batchSize = Math.min(
        this.regulator.batchSize,
        table.rowCount - row,
      );
      const before = this.cache.snapshot();

      for (let pass = 0; pass < passes; pass++) {
        for (let r = row; r < row + batchSize; r++) {
          const base = table.rowAddress(r);
          for (let off = 0; off < table.rowSizeBytes; off += this.cache.lineSize) {
            this.cache.access(base + off);
          }
          if (pass === 0) checksum += table.rowSum(r);
        }
      }

      const delta = CacheHierarchy.diff(this.cache.snapshot(), before);
      const l1HitRate = CacheHierarchy.l1HitRate(delta);
      const l2HitRate = CacheHierarchy.l2HitRate(delta);

      trace.push({
        batchIndex,
        startRow: row,
        rows: batchSize,
        batchSize,
        footprintBytes: batchSize * table.rowSizeBytes,
        fitsInL1: batchSize * table.rowSizeBytes <= this.cache.l1CapacityBytes,
        l1HitRate,
        l2HitRate,
        memoryReads: delta.memoryReads,
      });

      if (!fixed) {
        this.regulator.observe({ batchIndex, rows: batchSize, l1HitRate, l2HitRate });
      }
      row += batchSize;
      batchIndex++;
    }

    const stats = this.cache.snapshot();
    return {
      checksum,
      trace,
      stats,
      estimatedCycles: CacheHierarchy.estimatedCycles(stats),
      finalBatchSize: this.regulator.batchSize,
    };
  }
}
