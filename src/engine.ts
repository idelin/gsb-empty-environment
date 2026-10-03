/**
 * 简化的向量化执行引擎模型：
 * 查询以「批（Batch）」为单位流经一条 K 级算子流水线，
 * 每个算子都会顺序读写当前批次的全部行数据。
 * 每执行完一个批，就把缓存统计反馈给调节器，动态调整下一批的大小。
 */
import { AdaptiveBatchSizeRegulator, CacheStats } from "./regulator";
import { CacheHierarchy } from "./cache-model";

export interface BatchTrace {
  batchIndex: number;
  /** 本批实际处理的行数 */
  batchSize: number;
  rowSizeBytes: number;
  l1HitRate: number;
  l2HitRate: number;
  /** 估算的每行处理成本（CPU 周期） */
  cyclesPerRow: number;
  /** 本批执行后调节器给出的下一批大小 */
  nextBatchSize: number;
}

export class VectorizedEngine {
  constructor(
    readonly rowCount: number,
    readonly rowSizeBytes: number,
    readonly regulator: AdaptiveBatchSizeRegulator,
    readonly cache: CacheHierarchy,
    /** 流水线算子数量，默认 8 */
    readonly operatorCount: number = 8,
  ) {}

  run(): BatchTrace[] {
    const traces: BatchTrace[] = [];
    let offset = 0;
    let batchIndex = 0;

    while (offset < this.rowCount) {
      const n = Math.min(this.regulator.currentBatchSize, this.rowCount - offset);
      this.cache.resetStats();

      const base = offset * this.rowSizeBytes;
      for (let op = 0; op < this.operatorCount; op++) {
        this.cache.accessRange(base, n * this.rowSizeBytes);
      }

      const stats: CacheStats = this.cache.snapshot();
      const l1Total = stats.l1Hits + stats.l1Misses;
      const l2Total = stats.l2Hits + stats.l2Misses;
      const cycles = this.cache.estimateCycles(stats);

      const nextBatchSize = this.regulator.observe(stats, this.rowSizeBytes);

      traces.push({
        batchIndex,
        batchSize: n,
        rowSizeBytes: this.rowSizeBytes,
        l1HitRate: l1Total === 0 ? 1 : stats.l1Hits / l1Total,
        l2HitRate: l2Total === 0 ? 1 : stats.l2Hits / l2Total,
        cyclesPerRow: cycles / n,
        nextBatchSize,
      });

      offset += n;
      batchIndex++;
    }

    return traces;
  }
}
