/**
 * MatchIndex: keeps the occurrence sets of several literal patterns over a
 * ChunkedText, updated differentially on every edit.
 *
 * Edit-affected window (derivation; see README for the full version):
 * an edit replaces old [s, e) with new text of length L, delta = L - (e - s).
 * For a pattern of length m, a match starting at p covers [p, p + m).
 *  - p < s - (m - 1): the match lies entirely before s -> unchanged.
 *  - s - (m - 1) <= p < e: the match intersects the replaced range -> dropped.
 *  - p >= e: the match lies entirely after the edit -> shifted by delta.
 * New matches can therefore only start in [s - (m - 1), s + L), so the index
 * re-scans exactly that window (clamped to [0, length]) in the new text.
 *
 * MatchCursor: pausable k-way merge enumeration over several patterns.
 * Cursors created before an edit are adjusted differentially: a cursor past
 * the edit shifts by delta; a cursor inside the replaced range rewinds to the
 * affected-window start (and may re-emit updated matches there); a cursor
 * before the edit is untouched.
 */

import type { ChunkedText, EditRecord } from "./chunked-text.ts";
import { clampOffset, normalizePattern, searchRange } from "./search.ts";

export interface Match {
  readonly patternId: string;
  readonly start: number;
  /** start + pattern length (UTF-16 code units). */
  readonly end: number;
}

interface PatternEntry {
  readonly id: string;
  readonly pattern: string;
  readonly norm: string;
  readonly lower: boolean;
  matches: number[];
}

function lowerBound(arr: number[], off: number, strict: boolean): number {
  let lo = 0;
  let hi = arr.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (arr[mid] < off || (strict && arr[mid] === off)) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

export class MatchCursor {
  private lastOff: number;
  private lastPi = -1;
  private finished = false;
  private readonly entries: PatternEntry[];
  private readonly onClose: () => void;

  constructor(entries: PatternEntry[], from: number, onClose: () => void) {
    this.entries = entries;
    this.lastOff = from;
    this.onClose = onClose;
  }

  applyEdit(edit: EditRecord, windowLo: number): void {
    if (this.finished) return;
    if (this.lastOff >= edit.oldEnd) {
      this.lastOff += edit.delta;
    } else if (this.lastOff >= edit.start) {
      // Position fell inside the replaced range: rewind to the window start.
      this.lastOff = windowLo;
      this.lastPi = -1;
    }
  }

  /** Next match in (offset, pattern order); pausable between calls. */
  next(): IteratorResult<Match, undefined> {
    if (this.finished) return { value: undefined, done: true };
    let bestOff = Infinity;
    let bestPi = -1;
    for (let pi = 0; pi < this.entries.length; pi++) {
      const entry = this.entries[pi];
      const idx = lowerBound(entry.matches, this.lastOff, pi <= this.lastPi);
      if (idx < entry.matches.length && entry.matches[idx] < bestOff) {
        bestOff = entry.matches[idx];
        bestPi = pi;
      }
    }
    if (bestPi < 0) {
      this.close();
      return { value: undefined, done: true };
    }
    this.lastOff = bestOff;
    this.lastPi = bestPi;
    const entry = this.entries[bestPi];
    return {
      value: {
        patternId: entry.id,
        start: bestOff,
        end: bestOff + entry.norm.length,
      },
      done: false,
    };
  }

  close(): void {
    if (!this.finished) {
      this.finished = true;
      this.onClose();
    }
  }
}

export class MatchIndex {
  readonly text: ChunkedText;
  private readonly entries = new Map<string, PatternEntry>();
  private readonly cursors = new Set<MatchCursor>();

  constructor(text: ChunkedText) {
    this.text = text;
    text.addListener((edit) => this.applyEdit(edit));
  }

  addPattern(id: string, pattern: string, caseSensitive = true): void {
    if (this.entries.has(id)) throw new Error(`duplicate pattern id: ${id}`);
    const norm = normalizePattern(pattern, caseSensitive);
    const entry: PatternEntry = {
      id,
      pattern,
      norm,
      lower: !caseSensitive,
      matches: [...searchRange(this.text, norm, !caseSensitive, 0, this.text.length)],
    };
    this.entries.set(id, entry);
  }

  removePattern(id: string): void {
    this.entries.delete(id);
  }

  /** Sorted start offsets of `id` in the current text (UTF-16 offsets). */
  matches(id: string): number[] {
    return [...this.require(id).matches];
  }

  /**
   * Open a pausable cursor merging the matches of `patternIds`
   * (overlapping matches included), starting at UTF-16 offset `from`.
   */
  query(patternIds: string[], from = 0): MatchCursor {
    const entries = patternIds.map((id) => this.require(id));
    const cursor = new MatchCursor(entries, clampOffset(this.text, from), () => {
      this.cursors.delete(cursor);
    });
    this.cursors.add(cursor);
    return cursor;
  }

  private require(id: string): PatternEntry {
    const entry = this.entries.get(id);
    if (!entry) throw new Error(`unknown pattern id: ${id}`);
    return entry;
  }

  private maxPatternLength(): number {
    let m = 0;
    for (const entry of this.entries.values()) {
      m = Math.max(m, entry.norm.length);
    }
    return m;
  }

  private applyEdit(edit: EditRecord): void {
    for (const entry of this.entries.values()) {
      const m = entry.norm.length;
      if (m === 0) {
        // Empty pattern matches every position 0..length by definition.
        entry.matches = Array.from({ length: this.text.length + 1 }, (_, i) => i);
        continue;
      }
      const lo = Math.max(0, edit.start - (m - 1));
      const before: number[] = [];
      const after: number[] = [];
      for (const p of entry.matches) {
        if (p < lo) before.push(p);
        else if (p >= edit.oldEnd) after.push(p + edit.delta);
      }
      const hi = edit.newEnd - 1;
      const scanned =
        hi >= lo ? [...searchRange(this.text, entry.norm, entry.lower, lo, hi)] : [];
      // Ranges are disjoint and ordered: before < lo <= scanned < newEnd <= after.
      entry.matches = before.concat(scanned, after);
    }
    const windowLo = Math.max(0, edit.start - (this.maxPatternLength() - 1));
    for (const cursor of this.cursors) cursor.applyEdit(edit, windowLo);
  }
}
