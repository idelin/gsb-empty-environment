import type { Fraction } from './fraction.ts';

export interface OpenEntry {
  key: number;
  /** f = g + h, the primary ordering key. */
  f: Fraction;
  /** Exact path cost g at push time (used for stale detection and tie-breaks). */
  g: Fraction;
  seq: number;
}

/** Binary min-heap ordered by (f, g, seq). */
export class MinHeap {
  private readonly items: OpenEntry[] = [];

  get size(): number {
    return this.items.length;
  }

  /** Raw view of current entries (includes possibly stale ones). */
  raw(): readonly OpenEntry[] {
    return this.items;
  }

  push(entry: OpenEntry): void {
    this.items.push(entry);
    let i = this.items.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (!this.less(this.items[i], this.items[parent])) break;
      this.swap(i, parent);
      i = parent;
    }
  }

  pop(): OpenEntry | undefined {
    const n = this.items.length;
    if (n === 0) return undefined;
    const top = this.items[0];
    const last = this.items.pop()!;
    if (n > 1) {
      this.items[0] = last;
      let i = 0;
      for (;;) {
        const l = 2 * i + 1;
        const r = l + 1;
        let smallest = i;
        if (l < this.items.length && this.less(this.items[l], this.items[smallest])) smallest = l;
        if (r < this.items.length && this.less(this.items[r], this.items[smallest])) smallest = r;
        if (smallest === i) break;
        this.swap(i, smallest);
        i = smallest;
      }
    }
    return top;
  }

  private less(a: OpenEntry, b: OpenEntry): boolean {
    const cf = a.f.compare(b.f);
    if (cf !== 0) return cf < 0;
    const cg = a.g.compare(b.g);
    if (cg !== 0) return cg < 0;
    return a.seq < b.seq;
  }

  private swap(i: number, j: number): void {
    const t = this.items[i];
    this.items[i] = this.items[j];
    this.items[j] = t;
  }
}
