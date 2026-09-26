/**
 * Versioned prefix-height index over the visible row sequence.
 *
 * - Heights are updated incrementally; the prefix-sum array is truncated
 *   at the first edited slot and extended lazily on the next query, so
 *   edits are O(1) and queries pay O(n) only after a mutation.
 * - `version` bumps on every structural or height mutation. Consumers use
 *   it to detect that offsets/indices from an earlier version are stale.
 * - `indexAtOffset` is a binary search over a non-decreasing prefix array;
 *   it always terminates, even with runs of zero-height rows. Tie rule:
 *   the LAST row whose top edge is <= y wins (zero-height rows stacked at
 *   the same offset resolve to the bottom-most one).
 */
export class HeightIndex {
  private heights: number[] = [];
  private prefix: number[] = [0];
  readonly estimatedHeight: number;
  version = 0;

  constructor(estimatedHeight: number) {
    this.estimatedHeight = estimatedHeight;
  }

  get length(): number {
    return this.heights.length;
  }

  private touch(index: number): void {
    this.prefix.length = Math.min(this.prefix.length, index + 1);
    this.version += 1;
  }

  insertRows(index: number, heights: number[]): void {
    if (heights.length === 0) return;
    this.heights.splice(index, 0, ...heights);
    this.touch(index);
  }

  removeRows(index: number, count: number): void {
    if (count <= 0) return;
    this.heights.splice(index, count);
    this.touch(index);
  }

  moveRow(from: number, to: number): void {
    if (from === to) return;
    const [h] = this.heights.splice(from, 1);
    this.heights.splice(to, 0, h);
    this.touch(Math.min(from, to));
  }

  setHeight(index: number, height: number): void {
    if (this.heights[index] === height) return;
    this.heights[index] = height;
    this.touch(index);
  }

  heightAt(index: number): number {
    return this.heights[index];
  }

  private ensurePrefix(): void {
    const p = this.prefix;
    for (let i = p.length - 1; i < this.heights.length; i++) {
      p[i + 1] = p[i] + this.heights[i];
    }
  }

  totalHeight(): number {
    this.ensurePrefix();
    return this.prefix[this.heights.length];
  }

  /** Top edge (pixels from list start) of the row at `index`. */
  offsetOf(index: number): number {
    this.ensurePrefix();
    return this.prefix[index];
  }

  /**
   * Index of the row containing offset `y`. Returns -1 for an empty list.
   * Zero-height runs cannot cause an unbounded scan: this is a plain
   * binary search with a fixed iteration count.
   */
  indexAtOffset(y: number): number {
    this.ensurePrefix();
    const n = this.heights.length;
    if (n === 0) return -1;
    let lo = 0;
    let hi = n;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (this.prefix[mid] <= y) lo = mid;
      else hi = mid - 1;
    }
    return Math.min(lo, n - 1);
  }
}
