export class Fenwick {
  private tree: Float64Array;
  private size: number;

  constructor(values?: ArrayLike<number>) {
    this.size = values?.length ?? 0;
    this.tree = new Float64Array(this.size + 1);
    if (values) this.build(values);
  }

  get length(): number {
    return this.size;
  }

  reset(values: ArrayLike<number>): void {
    this.size = values.length;
    this.tree = new Float64Array(this.size + 1);
    this.build(values);
  }

  private build(values: ArrayLike<number>): void {
    for (let i = 0; i < this.size; i++) this.tree[i + 1] = values[i];
    for (let i = 1; i <= this.size; i++) {
      const parent = i + (i & -i);
      if (parent <= this.size) this.tree[parent] += this.tree[i];
    }
  }

  add(index: number, delta: number): void {
    for (let i = index + 1; i <= this.size; i += i & -i) {
      this.tree[i] += delta;
    }
  }

  prefixSum(count: number): number {
    let sum = 0;
    for (let i = count; i > 0; i -= i & -i) sum += this.tree[i];
    return sum;
  }

  total(): number {
    return this.prefixSum(this.size);
  }

  lowerBound(target: number): number {
    let index = 0;
    let remaining = target;
    let bit = 1;
    while ((bit << 1) <= this.size) bit <<= 1;
    for (; bit > 0; bit >>= 1) {
      const next = index + bit;
      if (next <= this.size && this.tree[next] <= remaining) {
        index = next;
        remaining -= this.tree[next];
      }
    }
    return index;
  }
}
