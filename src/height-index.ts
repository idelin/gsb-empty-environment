/**
 * 版本化前缀高度索引。
 *
 * 每次结构性修改（insert/remove/set）都会使 version 递增，
 * 异步测量等迟到更新可借此判定自己是否已过期。
 */
export interface PrefixIndex {
  /** 单调递增的版本号，任何修改都会使其 +1。 */
  readonly version: number;
  /** 行数。 */
  readonly size: number;
  insert(index: number, height: number): void;
  remove(index: number): void;
  set(index: number, height: number): void;
  heightAt(index: number): number;
  /** 前 count 行的高度之和，即第 count 行的起始偏移。 */
  prefix(count: number): number;
  /** 全部行高总和。 */
  total(): number;
  /**
   * 返回满足 prefix(i+1) > offset 的最小 i；若 offset >= total() 返回 size。
   * 零高行的累计和不会增加，因此会被自然跳过，查找必然终止。
   */
  findIndex(offset: number): number;
}

function assertNonNegative(height: number): void {
  if (!Number.isFinite(height) || height < 0) {
    throw new RangeError(`row height must be a finite number >= 0, got ${height}`);
  }
}

/** 基于 Fenwick 树（树状数组）的增量前缀高度索引。 */
export class FenwickIndex implements PrefixIndex {
  private tree: number[] = [];
  private heights: number[] = [];
  private highestBit = 1;
  version = 0;

  get size(): number {
    return this.heights.length;
  }

  private recomputeHighestBit(): void {
    let bit = 1;
    while (bit <= this.heights.length) bit <<= 1;
    this.highestBit = bit >> 1;
  }

  insert(index: number, height: number): void {
    assertNonNegative(height);
    if (index < 0 || index > this.heights.length) {
      throw new RangeError(`insert index ${index} out of range`);
    }
    this.heights.splice(index, 0, height);
    // Fenwick 树不支持中间插入，重建（O(n)），查询仍为 O(log n)。
    this.tree = new Array(this.heights.length + 1).fill(0);
    for (let i = 0; i < this.heights.length; i++) {
      this.addInternal(i, this.heights[i]);
    }
    this.recomputeHighestBit();
    this.version++;
  }

  remove(index: number): void {
    this.checkIndex(index, "remove");
    this.heights.splice(index, 1);
    this.tree = new Array(this.heights.length + 1).fill(0);
    for (let i = 0; i < this.heights.length; i++) {
      this.addInternal(i, this.heights[i]);
    }
    this.recomputeHighestBit();
    this.version++;
  }

  set(index: number, height: number): void {
    assertNonNegative(height);
    this.checkIndex(index, "set");
    const delta = height - this.heights[index];
    if (delta === 0) return;
    this.heights[index] = height;
    this.addInternal(index, delta);
    this.version++;
  }

  heightAt(index: number): number {
    this.checkIndex(index, "heightAt");
    return this.heights[index];
  }

  prefix(count: number): number {
    let n = Math.max(0, Math.min(count, this.heights.length));
    let sum = 0;
    while (n > 0) {
      sum += this.tree[n];
      n -= n & -n;
    }
    return sum;
  }

  total(): number {
    return this.prefix(this.heights.length);
  }

  findIndex(offset: number): number {
    if (offset < 0) return 0;
    // 找最大的 idx，使前 idx 项累计和 <= offset；答案即 idx（0 基）。
    // 零高项累计和不变，会被计入 idx 从而被跳过；bit 逐轮减半，必然终止。
    let idx = 0;
    let acc = 0;
    for (let bit = this.highestBit; bit > 0; bit >>= 1) {
      const next = idx + bit;
      if (next <= this.heights.length && acc + this.tree[next] <= offset) {
        idx = next;
        acc += this.tree[next];
      }
    }
    return idx;
  }

  private addInternal(index: number, delta: number): void {
    for (let n = index + 1; n <= this.heights.length; n += n & -n) {
      this.tree[n] += delta;
    }
  }

  private checkIndex(index: number, op: string): void {
    if (index < 0 || index >= this.heights.length) {
      throw new RangeError(`${op} index ${index} out of range (size ${this.heights.length})`);
    }
  }
}

/**
 * 独立的全量布局对照实现：每次查询都从头累加，
 * 不依赖任何增量状态，用作随机操作测试中的 oracle。
 */
export class NaiveIndex implements PrefixIndex {
  private heights: number[] = [];
  version = 0;

  get size(): number {
    return this.heights.length;
  }

  insert(index: number, height: number): void {
    assertNonNegative(height);
    if (index < 0 || index > this.heights.length) {
      throw new RangeError(`insert index ${index} out of range`);
    }
    this.heights.splice(index, 0, height);
    this.version++;
  }

  remove(index: number): void {
    this.checkIndex(index, "remove");
    this.heights.splice(index, 1);
    this.version++;
  }

  set(index: number, height: number): void {
    assertNonNegative(height);
    this.checkIndex(index, "set");
    if (this.heights[index] === height) return;
    this.heights[index] = height;
    this.version++;
  }

  heightAt(index: number): number {
    this.checkIndex(index, "heightAt");
    return this.heights[index];
  }

  prefix(count: number): number {
    const n = Math.max(0, Math.min(count, this.heights.length));
    let sum = 0;
    for (let i = 0; i < n; i++) sum += this.heights[i];
    return sum;
  }

  total(): number {
    return this.prefix(this.heights.length);
  }

  findIndex(offset: number): number {
    if (offset < 0) return 0;
    let acc = 0;
    for (let i = 0; i < this.heights.length; i++) {
      acc += this.heights[i];
      if (acc > offset) return i;
    }
    return this.heights.length;
  }

  private checkIndex(index: number, op: string): void {
    if (index < 0 || index >= this.heights.length) {
      throw new RangeError(`${op} index ${index} out of range (size ${this.heights.length})`);
    }
  }
}
