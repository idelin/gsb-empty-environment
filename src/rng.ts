/** mulberry32：确定性伪随机数发生器，相同 seed 产生相同序列。 */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export class Rng {
  private next: () => number;

  constructor(seed: number) {
    this.next = mulberry32(seed);
  }

  float(): number {
    return this.next();
  }

  /** [min, max] 闭区间整数。 */
  int(min: number, max: number): number {
    if (min > max) throw new Error(`Rng.int: 空区间 [${min}, ${max}]`);
    return min + Math.floor(this.next() * (max - min + 1));
  }

  pick<T>(items: readonly T[]): T {
    if (items.length === 0) throw new Error('Rng.pick: 空数组');
    return items[this.int(0, items.length - 1)];
  }

  bool(probability = 0.5): boolean {
    return this.next() < probability;
  }
}
