/**
 * 可复现的确定性伪随机数生成器 (mulberry32)。
 * 使用固定种子保证仿真与测试结果可复现。
 */
export class SeededRng {
  private state: number;

  constructor(seed: number) {
    this.state = seed >>> 0;
  }

  /** 返回 [0, 1) 区间均匀分布随机数。 */
  next(): number {
    this.state = (this.state + 0x6d2b79f5) >>> 0;
    let t = this.state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  }

  /** 返回 [0, n) 区间整数。 */
  int(n: number): number {
    return Math.floor(this.next() * n);
  }

  /** 从数组中均匀随机选取一个元素。 */
  pick<T>(items: readonly T[]): T {
    if (items.length === 0) {
      throw new Error('cannot pick from an empty array');
    }
    return items[this.int(items.length)];
  }
}
