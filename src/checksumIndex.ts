import {
  rowChecksum,
  xorChecksum,
  ZERO_CHECKSUM,
  type Row,
} from './rowHash.ts';
import type { TableStore } from './store.ts';

export interface DiffEntry {
  id: number;
  kind: 'tampered' | 'missing_in_target' | 'extra_in_target';
  sourceChecksum?: string;
  targetChecksum?: string;
}

export interface PartitionDiff {
  partitionId: number;
  diffs: DiffEntry[];
  /** 下钻过程中实际比对过的行数（验证只扫描了失配子树）。 */
  rowsScanned: number;
}

interface BucketState {
  checksum: string;
  count: number;
}

interface PartitionState {
  checksum: string;
  count: number;
  buckets: Map<number, BucketState>;
}

/**
 * 校验和索引：三层 Merkle 风格结构。
 *
 *   分区 (partition)  ── XOR ──▶ 分桶 (bucket)  ── XOR ──▶ 行 (row)
 *
 * 每个节点都是其子节点校验和的异或值。XOR 满足交换律/结合律且
 * 自逆（x ^ x = 0），因此行变更只需「异或掉旧值、异或入新值」，
 * O(1) 增量维护，无需重扫数据 —— 变更流持续涌入也能实时滚动。
 */
export class ChecksumIndex {
  readonly side: string;
  readonly partitionSize: number;
  readonly bucketSize: number;

  private readonly partitions = new Map<number, PartitionState>();
  private readonly rowChecksums = new Map<number, string>();

  constructor(side: string, opts: { partitionSize?: number; bucketSize?: number } = {}) {
    this.side = side;
    this.partitionSize = opts.partitionSize ?? 4096;
    this.bucketSize = opts.bucketSize ?? 256;
  }

  /** 订阅某个库表的变更流，开始增量维护校验和。 */
  track(store: TableStore): void {
    store.onChange((event) => {
      if (event.type === 'upsert') {
        this.applyUpsert(event.id, rowChecksum(event.row));
      } else {
        this.applyDelete(event.id);
      }
    });
  }

  applyUpsert(id: number, checksum: string): void {
    const old = this.rowChecksums.get(id);
    if (old !== undefined) this.xorOut(id, old);
    this.xorIn(id, checksum);
    this.rowChecksums.set(id, checksum);
  }

  applyDelete(id: number): void {
    const old = this.rowChecksums.get(id);
    if (old === undefined) return;
    this.xorOut(id, old);
    this.rowChecksums.delete(id);
  }

  get rowCount(): number {
    return this.rowChecksums.size;
  }

  partitionOf(id: number): number {
    return Math.floor(id / this.partitionSize);
  }

  private bucketOf(id: number): number {
    return Math.floor(id / this.bucketSize);
  }

  /** 顶层摘要：partitionId -> 分区校验和。一次巡检只需比对这张小表。 */
  digest(): Map<number, string> {
    const out = new Map<number, string>();
    for (const [pid, p] of this.partitions) out.set(pid, p.checksum);
    return out;
  }

  /**
   * 与另一侧索引在指定分区内下钻比对：
   * 先比分桶校验和，仅对失配分桶逐行比对，精确定位主键。
   */
  diffPartition(other: ChecksumIndex, partitionId: number): PartitionDiff {
    const diffs: DiffEntry[] = [];
    let rowsScanned = 0;

    const mine = this.partitions.get(partitionId);
    const theirs = other.partitions.get(partitionId);
    const bucketIds = new Set<number>([
      ...(mine?.buckets.keys() ?? []),
      ...(theirs?.buckets.keys() ?? []),
    ]);

    for (const bucketId of bucketIds) {
      const myBucket = mine?.buckets.get(bucketId);
      const theirBucket = theirs?.buckets.get(bucketId);
      const mySum = myBucket?.checksum ?? ZERO_CHECKSUM;
      const theirSum = theirBucket?.checksum ?? ZERO_CHECKSUM;
      if (mySum === theirSum) continue; // 分桶一致，整桶跳过

      const lo = bucketId * this.bucketSize;
      const hi = lo + this.bucketSize;
      const ids = new Set<number>();
      for (const id of this.rowChecksums.keys()) {
        if (id >= lo && id < hi) ids.add(id);
      }
      for (const id of other.rowChecksums.keys()) {
        if (id >= lo && id < hi) ids.add(id);
      }

      for (const id of ids) {
        rowsScanned++;
        const a = this.rowChecksums.get(id);
        const b = other.rowChecksums.get(id);
        if (a === b) continue;
        if (a !== undefined && b !== undefined) {
          diffs.push({ id, kind: 'tampered', sourceChecksum: a, targetChecksum: b });
        } else if (a !== undefined) {
          diffs.push({ id, kind: 'missing_in_target', sourceChecksum: a });
        } else {
          diffs.push({ id, kind: 'extra_in_target', targetChecksum: b });
        }
      }
    }

    diffs.sort((x, y) => x.id - y.id);
    return { partitionId, diffs, rowsScanned };
  }

  private xorIn(id: number, checksum: string): void {
    const pid = this.partitionOf(id);
    const bid = this.bucketOf(id);
    let p = this.partitions.get(pid);
    if (!p) {
      p = { checksum: ZERO_CHECKSUM, count: 0, buckets: new Map() };
      this.partitions.set(pid, p);
    }
    let b = p.buckets.get(bid);
    if (!b) {
      b = { checksum: ZERO_CHECKSUM, count: 0 };
      p.buckets.set(bid, b);
    }
    p.checksum = xorChecksum(p.checksum, checksum);
    p.count++;
    b.checksum = xorChecksum(b.checksum, checksum);
    b.count++;
  }

  private xorOut(id: number, checksum: string): void {
    const pid = this.partitionOf(id);
    const bid = this.bucketOf(id);
    const p = this.partitions.get(pid);
    if (!p) return;
    const b = p.buckets.get(bid);
    p.checksum = xorChecksum(p.checksum, checksum);
    p.count--;
    if (b) {
      b.checksum = xorChecksum(b.checksum, checksum);
      b.count--;
      if (b.count === 0) p.buckets.delete(bid);
    }
    if (p.count === 0) this.partitions.delete(pid);
  }
}
