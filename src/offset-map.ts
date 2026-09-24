/**
 * Run-length compressed bidirectional map between absolute byte offsets and
 * UTF-16 code-unit offsets.
 *
 * The map is built incrementally by appending segments. A segment maps a
 * byte range [byteStart, byteEnd) to a UTF-16 range [u16Start, u16End):
 *  - a valid scalar value: 1-4 bytes -> 1 or 2 UTF-16 code units
 *  - a replacement (U+FFFD) for an invalid sequence: k bytes -> 1 code unit
 *    (a many-to-one mapping, explicitly surfaced as a coverage interval)
 *
 * Consecutive identical segments are coalesced into runs, so lookups are
 * O(log runs) by binary search and never require re-decoding any input.
 */

export interface CoverageInterval {
  byteStart: number;
  byteEnd: number;
  u16Start: number;
  u16End: number;
}

export interface RangeMapping {
  /** Snapped-outward start in the target space. */
  start: number;
  /** Snapped-outward end in the target space. */
  end: number;
  /** Many-to-one replacement coverage intervals overlapping the range. */
  replacements: CoverageInterval[];
}

/** Serializable form of one run: [byteStart, u16Start, count, byteLen, u16Len, isReplacement]. */
export type RunTuple = [number, number, number, number, number, number];

interface Run {
  byteStart: number;
  u16Start: number;
  count: number;
  byteLen: number;
  u16Len: number;
  replacement: boolean;
}

export interface Segment {
  byteStart: number;
  byteEnd: number;
  u16Start: number;
  u16End: number;
  replacement: boolean;
}

export class OffsetMap {
  private runs: Run[] = [];
  private byteLenTotal = 0;
  private u16LenTotal = 0;

  get byteLength(): number {
    return this.byteLenTotal;
  }

  get u16Length(): number {
    return this.u16LenTotal;
  }

  /** Number of compressed runs (a pure-ASCII stream of any size is 1 run). */
  get runCount(): number {
    return this.runs.length;
  }

  append(byteLen: number, u16Len: number, replacement: boolean): void {
    const last = this.runs[this.runs.length - 1];
    if (
      last !== undefined &&
      last.byteLen === byteLen &&
      last.u16Len === u16Len &&
      last.replacement === replacement
    ) {
      last.count += 1;
    } else {
      this.runs.push({
        byteStart: this.byteLenTotal,
        u16Start: this.u16LenTotal,
        count: 1,
        byteLen,
        u16Len,
        replacement,
      });
    }
    this.byteLenTotal += byteLen;
    this.u16LenTotal += u16Len;
  }

  /**
   * Map a byte offset to a UTF-16 offset. Offsets inside a multi-byte
   * character resolve to the start of that character; offsets exactly on a
   * boundary map exactly.
   */
  byteToU16(offset: number): number {
    if (offset <= 0) return 0;
    if (offset >= this.byteLenTotal) return this.u16LenTotal;
    return this.segmentAtByte(offset).u16Start;
  }

  /**
   * Map a UTF-16 offset to a byte offset. Offsets inside a character
   * (including between the halves of a surrogate pair) resolve to the start
   * of that character, so a surrogate pair is never split.
   */
  u16ToByte(offset: number): number {
    if (offset <= 0) return 0;
    if (offset >= this.u16LenTotal) return this.byteLenTotal;
    return this.segmentAtU16(offset).byteStart;
  }

  /**
   * Map a byte range to a UTF-16 range, snapping outward to character
   * boundaries. Many-to-one replacement coverage intervals overlapping the
   * range are returned explicitly.
   */
  byteRangeToU16(start: number, end: number): RangeMapping {
    const total = this.byteLenTotal;
    const s = Math.max(0, Math.min(start, total));
    const e = Math.max(s, Math.min(end, total));
    let mappedEnd: number;
    if (e >= total) {
      mappedEnd = this.u16LenTotal;
    } else {
      const seg = this.segmentAtByte(e);
      mappedEnd = e === seg.byteStart ? seg.u16Start : seg.u16End;
    }
    return { start: this.byteToU16(s), end: mappedEnd, replacements: this.replacementsBetween(s, e) };
  }

  /**
   * Map a UTF-16 range to a byte range, snapping outward to character
   * boundaries (surrogate pairs are never split). Many-to-one replacement
   * coverage intervals overlapping the mapped byte range are returned
   * explicitly.
   */
  u16RangeToByte(start: number, end: number): RangeMapping {
    const total = this.u16LenTotal;
    const s = Math.max(0, Math.min(start, total));
    const e = Math.max(s, Math.min(end, total));
    let mappedEnd: number;
    if (e >= total) {
      mappedEnd = this.byteLenTotal;
    } else {
      const seg = this.segmentAtU16(e);
      mappedEnd = e === seg.u16Start ? seg.byteStart : seg.byteEnd;
    }
    const byteStart = this.u16ToByte(s);
    return { start: byteStart, end: mappedEnd, replacements: this.replacementsBetween(byteStart, mappedEnd) };
  }

  /** Iterate the uncompressed segments in order. */
  *segments(): Generator<Segment> {
    for (const run of this.runs) {
      for (let i = 0; i < run.count; i++) {
        const byteStart = run.byteStart + i * run.byteLen;
        const u16Start = run.u16Start + i * run.u16Len;
        yield {
          byteStart,
          byteEnd: byteStart + run.byteLen,
          u16Start,
          u16End: u16Start + run.u16Len,
          replacement: run.replacement,
        };
      }
    }
  }

  toJSON(): RunTuple[] {
    return this.runs.map((r) => [
      r.byteStart,
      r.u16Start,
      r.count,
      r.byteLen,
      r.u16Len,
      r.replacement ? 1 : 0,
    ]);
  }

  restore(tuples: RunTuple[]): void {
    this.runs = tuples.map(([byteStart, u16Start, count, byteLen, u16Len, rep]) => ({
      byteStart,
      u16Start,
      count,
      byteLen,
      u16Len,
      replacement: rep === 1,
    }));
    this.byteLenTotal = 0;
    this.u16LenTotal = 0;
    for (const run of this.runs) {
      this.byteLenTotal += run.count * run.byteLen;
      this.u16LenTotal += run.count * run.u16Len;
    }
  }

  static fromJSON(tuples: RunTuple[]): OffsetMap {
    const map = new OffsetMap();
    map.restore(tuples);
    return map;
  }

  private segmentAtByte(offset: number): Segment {
    const run = this.runs[this.findRunByByte(offset)];
    const index = Math.min(Math.floor((offset - run.byteStart) / run.byteLen), run.count - 1);
    const byteStart = run.byteStart + index * run.byteLen;
    const u16Start = run.u16Start + index * run.u16Len;
    return {
      byteStart,
      byteEnd: byteStart + run.byteLen,
      u16Start,
      u16End: u16Start + run.u16Len,
      replacement: run.replacement,
    };
  }

  private segmentAtU16(offset: number): Segment {
    const run = this.runs[this.findRunByU16(offset)];
    const index = Math.min(Math.floor((offset - run.u16Start) / run.u16Len), run.count - 1);
    const byteStart = run.byteStart + index * run.byteLen;
    const u16Start = run.u16Start + index * run.u16Len;
    return {
      byteStart,
      byteEnd: byteStart + run.byteLen,
      u16Start,
      u16End: u16Start + run.u16Len,
      replacement: run.replacement,
    };
  }

  /** Index of the last run whose byteStart <= offset. */
  private findRunByByte(offset: number): number {
    let lo = 0;
    let hi = this.runs.length - 1;
    let ans = 0;
    while (lo <= hi) {
      const mid = (lo + hi) >> 1;
      if (this.runs[mid].byteStart <= offset) {
        ans = mid;
        lo = mid + 1;
      } else {
        hi = mid - 1;
      }
    }
    return ans;
  }

  /** Index of the last run whose u16Start <= offset. */
  private findRunByU16(offset: number): number {
    let lo = 0;
    let hi = this.runs.length - 1;
    let ans = 0;
    while (lo <= hi) {
      const mid = (lo + hi) >> 1;
      if (this.runs[mid].u16Start <= offset) {
        ans = mid;
        lo = mid + 1;
      } else {
        hi = mid - 1;
      }
    }
    return ans;
  }

  /** Many-to-one replacement segments (byteLen > 1 -> 1 code unit) overlapping [byteStart, byteEnd). */
  private replacementsBetween(byteStart: number, byteEnd: number): CoverageInterval[] {
    const out: CoverageInterval[] = [];
    if (this.runs.length === 0 || byteEnd <= byteStart) return out;
    const first = this.findRunByByte(byteStart);
    for (let i = first; i < this.runs.length; i++) {
      const run = this.runs[i];
      if (run.byteStart >= byteEnd) break;
      if (!run.replacement || run.byteLen <= 1) continue;
      const startIndex = Math.max(0, Math.ceil((byteStart - run.byteStart) / run.byteLen));
      for (let k = startIndex; k < run.count; k++) {
        const segStart = run.byteStart + k * run.byteLen;
        if (segStart >= byteEnd) break;
        out.push({
          byteStart: segStart,
          byteEnd: segStart + run.byteLen,
          u16Start: run.u16Start + k * run.u16Len,
          u16End: run.u16Start + k * run.u16Len + run.u16Len,
        });
      }
    }
    return out;
  }
}
