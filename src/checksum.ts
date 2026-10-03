/**
 * checksum.ts — 分区滚动 XOR 校验和索引 + Merkle 树。
 *
 * 设计要点：
 *  - 全表按主键哈希划分为固定数量分区（partition）。
 *  - 每个分区维护一个滚动 XOR 校验和：
 *      插入  -> checksum ^= hash(row)
 *      删除  -> checksum ^= hash(row)   （XOR 自逆）
 *      更新  -> checksum ^= hash(old) ^ hash(new)
 *    因此变更持续流动时无需重扫全表，O(1) 增量维护。
 *  - 分区校验和作为叶子构建 Merkle 树，根哈希用于 O(1) 的一线比对；
 *    根不一致时逐叶子下钻，毫秒级定位到失配分区。
 */
import { createHash } from "node:crypto";
import { hashString, hashToHex, type Hash64 } from "./hash.ts";

export class ChecksumIndex {
  readonly partitionCount: number;
  private readonly hi: Uint32Array;
  private readonly lo: Uint32Array;
  private readonly rowCounts: Uint32Array;

  constructor(partitionCount = 1024) {
    if (partitionCount <= 0 || (partitionCount & (partitionCount - 1)) !== 0) {
      throw new Error("partitionCount 必须是 2 的幂");
    }
    this.partitionCount = partitionCount;
    this.hi = new Uint32Array(partitionCount);
    this.lo = new Uint32Array(partitionCount);
    this.rowCounts = new Uint32Array(partitionCount);
  }

  /** 主键 -> 分区号（对两侧库确定性一致）。 */
  partitionOf(pk: string): number {
    const [h2, h1] = hashString(pk, 0x9e3779b9);
    return ((h1 ^ h2) >>> 0) & (this.partitionCount - 1);
  }

  /**
   * 增量应用一次行变更。
   * @param oldHash 变更前行哈希（插入时传 null）
   * @param newHash 变更后行哈希（删除时传 null）
   */
  apply(pk: string, oldHash: Hash64 | null, newHash: Hash64 | null): void {
    const p = this.partitionOf(pk);
    if (oldHash !== null) {
      this.hi[p] = (this.hi[p] ^ oldHash[0]) >>> 0;
      this.lo[p] = (this.lo[p] ^ oldHash[1]) >>> 0;
      this.rowCounts[p]--;
    }
    if (newHash !== null) {
      this.hi[p] = (this.hi[p] ^ newHash[0]) >>> 0;
      this.lo[p] = (this.lo[p] ^ newHash[1]) >>> 0;
      this.rowCounts[p]++;
    }
  }

  leaf(p: number): Hash64 {
    return [this.hi[p], this.lo[p]];
  }

  rowCount(p: number): number {
    return this.rowCounts[p];
  }

  totalRows(): number {
    let n = 0;
    for (let i = 0; i < this.partitionCount; i++) n += this.rowCounts[i];
    return n;
  }

  /** 返回与另一索引校验和不一致的分区号列表。 */
  mismatchingPartitions(other: ChecksumIndex): number[] {
    if (other.partitionCount !== this.partitionCount) {
      throw new Error("两侧 partitionCount 不一致，无法比对");
    }
    const bad: number[] = [];
    for (let p = 0; p < this.partitionCount; p++) {
      if (
        this.hi[p] !== other.hi[p] ||
        this.lo[p] !== other.lo[p] ||
        this.rowCounts[p] !== other.rowCounts[p]
      ) {
        bad.push(p);
      }
    }
    return bad;
  }

  /** Merkle 根哈希：叶子为各分区校验和，逐层 SHA-256 聚合。 */
  rootHash(): string {
    let level: string[] = [];
    for (let p = 0; p < this.partitionCount; p++) {
      level.push(
        hashToHex([this.hi[p], this.lo[p]]) +
          ":" +
          this.rowCounts[p].toString(16),
      );
    }
    while (level.length > 1) {
      const next: string[] = [];
      for (let i = 0; i < level.length; i += 2) {
        next.push(
          createHash("sha256")
            .update(level[i] + "|" + level[i + 1])
            .digest("hex"),
        );
      }
      level = next;
    }
    return level[0];
  }
}
