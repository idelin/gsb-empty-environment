/**
 * 确定性伪随机数发生器 (mulberry32)。
 * 状态仅为一个 uint32，可完整序列化/恢复 —— 这是可重放性的前提。
 */
export class Rng {
  private state: number;

  constructor(seed: number) {
    this.state = seed >>> 0;
  }

  /** 返回 [0, 1) 区间伪随机数 */
  next(): number {
    this.state = (this.state + 0x6d2b79f5) >>> 0;
    let t = this.state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  }

  /** 返回 [0, bound) 区间伪随机整数 */
  nextInt(bound: number): number {
    return Math.floor(this.next() * bound);
  }

  getState(): number {
    return this.state;
  }

  setState(state: number): void {
    this.state = state >>> 0;
  }
}
