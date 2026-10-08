/**
 * Segment tree over the series holding, per node, a summary of the valid
 * points in its index range. All zoom levels share this one structure: a
 * viewport query merges O(log n) nodes per pixel column instead of scanning
 * raw points, and a local edit re-propagates in O(log n).
 */
export interface NodeSummary {
  count: number;
  /** Smallest valid index in range (-1 when empty). */
  firstIdx: number;
  /** Largest valid index in range (-1 when empty). */
  lastIdx: number;
  /** Index of the minimum value; ties resolve to the smaller index. */
  minIdx: number;
  /** Index of the maximum value; ties resolve to the smaller index. */
  maxIdx: number;
}

export const EMPTY_SUMMARY: NodeSummary = {
  count: 0,
  firstIdx: -1,
  lastIdx: -1,
  minIdx: -1,
  maxIdx: -1,
};

export class SummaryTree {
  private readonly n: number;
  private readonly size: number;
  private readonly values: ArrayLike<number | null>;
  private readonly count: Int32Array;
  private readonly first: Int32Array;
  private readonly last: Int32Array;
  private readonly minIdx: Int32Array;
  private readonly maxIdx: Int32Array;

  constructor(values: ArrayLike<number | null>) {
    this.values = values;
    this.n = values.length;
    this.size = Math.max(1, this.n);
    const total = 2 * this.size;
    this.count = new Int32Array(total);
    this.first = new Int32Array(total).fill(-1);
    this.last = new Int32Array(total).fill(-1);
    this.minIdx = new Int32Array(total).fill(-1);
    this.maxIdx = new Int32Array(total).fill(-1);
    for (let i = 0; i < this.n; i++) this.writeLeaf(i);
    for (let i = this.size - 1; i >= 1; i--) this.pull(i);
  }

  get length(): number {
    return this.n;
  }

  private isValid(i: number): boolean {
    const v = this.values[i];
    return v !== null && v !== undefined && !Number.isNaN(v);
  }

  private writeLeaf(i: number): void {
    const at = this.size + i;
    if (this.isValid(i)) {
      this.count[at] = 1;
      this.first[at] = i;
      this.last[at] = i;
      this.minIdx[at] = i;
      this.maxIdx[at] = i;
    } else {
      this.count[at] = 0;
      this.first[at] = -1;
      this.last[at] = -1;
      this.minIdx[at] = -1;
      this.maxIdx[at] = -1;
    }
  }

  private pickMin(i: number, j: number): number {
    if (i < 0) return j;
    if (j < 0) return i;
    const a = this.values[i] as number;
    const b = this.values[j] as number;
    if (a < b) return i;
    if (b < a) return j;
    return Math.min(i, j);
  }

  private pickMax(i: number, j: number): number {
    if (i < 0) return j;
    if (j < 0) return i;
    const a = this.values[i] as number;
    const b = this.values[j] as number;
    if (a > b) return i;
    if (b > a) return j;
    return Math.min(i, j);
  }

  private pull(i: number): void {
    const l = 2 * i;
    const r = 2 * i + 1;
    this.count[i] = this.count[l] + this.count[r];
    this.first[i] = this.first[l] >= 0 ? this.first[l] : this.first[r];
    this.last[i] = this.last[r] >= 0 ? this.last[r] : this.last[l];
    this.minIdx[i] = this.pickMin(this.minIdx[l], this.minIdx[r]);
    this.maxIdx[i] = this.pickMax(this.maxIdx[l], this.maxIdx[r]);
  }

  /** Reflect a value change at index i. O(log n). */
  update(i: number): void {
    this.writeLeaf(i);
    for (let at = (this.size + i) >> 1; at >= 1; at >>= 1) this.pull(at);
  }

  private nodeAt(i: number): NodeSummary {
    return {
      count: this.count[i],
      firstIdx: this.first[i],
      lastIdx: this.last[i],
      minIdx: this.minIdx[i],
      maxIdx: this.maxIdx[i],
    };
  }

  private merge(a: NodeSummary, b: NodeSummary): NodeSummary {
    if (a.count === 0) return b;
    if (b.count === 0) return a;
    return {
      count: a.count + b.count,
      firstIdx: a.firstIdx,
      lastIdx: b.lastIdx,
      minIdx: this.pickMin(a.minIdx, b.minIdx),
      maxIdx: this.pickMax(a.maxIdx, b.maxIdx),
    };
  }

  /** Summary of valid points with index in [lo, hi] inclusive. O(log n). */
  query(lo: number, hi: number): NodeSummary {
    if (lo > hi || lo >= this.n || hi < 0) return EMPTY_SUMMARY;
    let left: NodeSummary = EMPTY_SUMMARY;
    let right: NodeSummary = EMPTY_SUMMARY;
    let l = Math.max(0, lo) + this.size;
    let r = Math.min(this.n - 1, hi) + this.size + 1;
    while (l < r) {
      if (l & 1) {
        left = this.merge(left, this.nodeAt(l));
        l++;
      }
      if (r & 1) {
        r--;
        right = this.merge(this.nodeAt(r), right);
      }
      l >>= 1;
      r >>= 1;
    }
    return this.merge(left, right);
  }
}
