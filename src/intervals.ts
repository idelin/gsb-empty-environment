/**
 * A set of disjoint half-open byte intervals [start, end), kept sorted and merged.
 */
export class IntervalSet {
  private intervals: Array<[number, number]> = [];

  static fromJSON(rows: Array<[number, number]>): IntervalSet {
    const set = new IntervalSet();
    for (const row of rows) set.add(row[0], row[1]);
    return set;
  }

  add(start: number, end: number): void {
    if (end <= start) return;
    const out: Array<[number, number]> = [];
    let s = start;
    let e = end;
    for (const [a, b] of this.intervals) {
      if (b < s) {
        out.push([a, b]);
      } else if (e < a) {
        out.push([s, e]);
        s = a;
        e = b;
      } else {
        s = Math.min(s, a);
        e = Math.max(e, b);
      }
    }
    out.push([s, e]);
    out.sort((x, y) => x[0] - y[0]);
    this.intervals = out;
  }

  /** Portions of [start, end) already covered by this set. */
  overlaps(start: number, end: number): Array<[number, number]> {
    const out: Array<[number, number]> = [];
    for (const [a, b] of this.intervals) {
      const s = Math.max(a, start);
      const e = Math.min(b, end);
      if (s < e) out.push([s, e]);
    }
    return out;
  }

  coveredLength(): number {
    let total = 0;
    for (const [a, b] of this.intervals) total += b - a;
    return total;
  }

  isComplete(size: number): boolean {
    const first = this.intervals[0];
    return this.intervals.length === 1 && first !== undefined && first[0] === 0 && first[1] === size;
  }

  /** Uncovered ranges within [0, size). */
  gaps(size: number): Array<[number, number]> {
    const out: Array<[number, number]> = [];
    let cursor = 0;
    for (const [a, b] of this.intervals) {
      if (a > cursor) out.push([cursor, Math.min(a, size)]);
      cursor = Math.max(cursor, b);
    }
    if (cursor < size) out.push([cursor, size]);
    return out;
  }

  toJSON(): Array<[number, number]> {
    return this.intervals.map(([a, b]) => [a, b]);
  }
}
