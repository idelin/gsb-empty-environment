import { performance } from 'node:perf_hooks';
import type { ChecksumIndex, DiffEntry } from './checksumIndex.ts';

export interface PatrolReport {
  cycle: number;
  partitionsChecked: number;
  mismatchedPartitions: number[];
  diffs: DiffEntry[];
  /** 下钻阶段实际逐行比对的行数（远小于全量行数即为高效）。 */
  rowsScanned: number;
  durationMs: number;
  consistent: boolean;
}

/**
 * 在线巡检器：周期性比对源/目标两端的校验和摘要。
 *
 * 每个巡检周期：
 *   1. 交换双方顶层摘要（partitionId -> XOR 校验和），O(分区数) 比对；
 *   2. 对失配分区沿 Merkle 结构下钻（分区 -> 分桶 -> 行），
 *      只扫描失配子树，毫秒级定位到具体主键。
 */
export class Inspector {
  private readonly source: ChecksumIndex;
  private readonly target: ChecksumIndex;
  private cycle = 0;

  constructor(source: ChecksumIndex, target: ChecksumIndex) {
    this.source = source;
    this.target = target;
  }

  /** 执行一次巡检，返回比对报告。 */
  patrol(): PatrolReport {
    const started = performance.now();
    this.cycle++;

    const sourceDigest = this.source.digest();
    const targetDigest = this.target.digest();
    const partitionIds = new Set<number>([
      ...sourceDigest.keys(),
      ...targetDigest.keys(),
    ]);

    const mismatchedPartitions: number[] = [];
    const diffs: DiffEntry[] = [];
    let rowsScanned = 0;

    for (const pid of partitionIds) {
      if (sourceDigest.get(pid) === targetDigest.get(pid)) continue;
      mismatchedPartitions.push(pid);
      const result = this.source.diffPartition(this.target, pid);
      diffs.push(...result.diffs);
      rowsScanned += result.rowsScanned;
    }

    mismatchedPartitions.sort((a, b) => a - b);
    diffs.sort((a, b) => a.id - b.id);

    return {
      cycle: this.cycle,
      partitionsChecked: partitionIds.size,
      mismatchedPartitions,
      diffs,
      rowsScanned,
      durationMs: performance.now() - started,
      consistent: mismatchedPartitions.length === 0,
    };
  }
}
