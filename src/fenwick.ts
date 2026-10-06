/**
 * Fenwick tree (binary indexed tree) over non-negative values.
 * Supports point updates and prefix-sum queries in O(log n),
 * plus locating an item by offset within the cumulative sum.
 */
export class FenwickTree {
  /** 1-based internal storage; tree[0] is unused. */
  private tree: number[] = [0];

  static from(values: readonly number[]): FenwickTree {
    const fenwick = new FenwickTree();
    fenwick.tree = new Array(values.length + 1).fill(0);
    for (let i = 0; i < values.length; i++) {
      fenwick.tree[i + 1] = values[i]!;
    }
    for (let i = 1; i <= values.length; i++) {
      const parent = i + (i & -i);
      if (parent <= values.length) {
        fenwick.tree[parent]! += fenwick.tree[i]!;
      }
    }
    return fenwick;
  }

  get size(): number {
    return this.tree.length - 1;
  }

  /** Adds `delta` to the value at 0-based `index`. */
  add(index: number, delta: number): void {
    for (let i = index + 1; i < this.tree.length; i += i & -i) {
      this.tree[i]! += delta;
    }
  }

  /** Sum of the first `count` values, `count` in [0, size]. */
  sum(count: number): number {
    let total = 0;
    for (let i = count; i > 0; i -= i & -i) {
      total += this.tree[i]!;
    }
    return total;
  }

  total(): number {
    return this.sum(this.size);
  }

  /**
   * Largest count c in [0, size] such that sum(c) <= value.
   * Requires all values to be non-negative.
   */
  maxPrefixLeq(value: number): number {
    let index = 0;
    let bit = highestPowerOfTwo(this.size);
    while (bit !== 0) {
      const next = index + bit;
      if (next <= this.size && this.tree[next]! <= value) {
        value -= this.tree[next]!;
        index = next;
      }
      bit >>= 1;
    }
    return index;
  }
}

function highestPowerOfTwo(n: number): number {
  let bit = 1;
  while (bit << 1 <= n) {
    bit <<= 1;
  }
  return bit;
}
