/**
 * UTC working-time calendar.
 *
 * All timestamps are epoch milliseconds. Intervals are half-open [start, end):
 * finishing exactly at an interval's right endpoint is a legal completion.
 */

export interface Interval {
  /** epoch ms, inclusive */
  start: number;
  /** epoch ms, exclusive */
  end: number;
}

export interface AccumulateResult {
  /** cursor the accumulation started from */
  from: number;
  /** final cursor position after accumulating */
  end: number;
  /** 1 = forward in time, -1 = backward (negative duration) */
  direction: 1 | -1;
  /** absolute duration that was requested, in ms */
  requestedMs: number;
  /** open time actually consumed, in ms */
  accumulatedMs: number;
  /** open time that could not be fulfilled (0 when complete) */
  remainingMs: number;
  /** true when the full requested duration was accumulated */
  complete: boolean;
  /** traversed open sub-intervals, chronological, non-overlapping */
  evidence: Interval[];
}

export const MS_PER_MINUTE = 60_000;
export const MS_PER_DAY = 86_400_000;

/** Merge overlapping or touching intervals into a sorted disjoint union. */
export function unionIntervals(intervals: Interval[]): Interval[] {
  const sorted = intervals
    .filter((iv) => iv.end > iv.start)
    .map((iv) => ({ start: iv.start, end: iv.end }))
    .sort((a, b) => a.start - b.start || a.end - b.end);
  const merged: Interval[] = [];
  for (const iv of sorted) {
    const last = merged[merged.length - 1];
    if (last && iv.start <= last.end) {
      if (iv.end > last.end) last.end = iv.end;
    } else {
      merged.push(iv);
    }
  }
  return merged;
}

/** UTC day range for a 'YYYY-MM-DD' string, as a half-open interval. */
export function utcDayRange(day: string): Interval {
  const start = Date.parse(`${day}T00:00:00.000Z`);
  if (Number.isNaN(start)) {
    throw new Error(`Invalid UTC day: ${day}`);
  }
  return { start, end: start + MS_PER_DAY };
}

/** Subtract one blocking interval from a sorted disjoint interval list. */
export function subtractInterval(open: Interval[], block: Interval): Interval[] {
  const out: Interval[] = [];
  for (const iv of open) {
    if (block.end <= iv.start || block.start >= iv.end) {
      out.push(iv);
      continue;
    }
    if (block.start > iv.start) out.push({ start: iv.start, end: block.start });
    if (block.end < iv.end) out.push({ start: block.end, end: iv.end });
  }
  return out;
}

/** Remove every pause day's full UTC day from the open intervals. */
export function subtractPauseDays(open: Interval[], pauseDays: string[]): Interval[] {
  let result = open;
  for (const day of pauseDays) {
    result = subtractInterval(result, utcDayRange(day));
  }
  return result;
}

/** Effective calendar: union of open intervals with pause days carved out. */
export function buildEffectiveCalendar(open: Interval[], pauseDays: string[] = []): Interval[] {
  return subtractPauseDays(unionIntervals(open), pauseDays);
}

/**
 * Accumulate `durationMs` of open time starting at cursor `from`.
 *
 * - Positive duration walks forward; negative duration walks backward
 *   (deduction), consuming open time to the left of `from`.
 * - Overlapping open intervals are unioned first, so shared time is
 *   never counted twice.
 * - Pause days remove the whole UTC day from the open set.
 * - Completing exactly on an interval boundary is legal: with half-open
 *   intervals the cursor simply lands on the boundary.
 * - The returned `evidence` lists every open sub-interval consumed, in
 *   chronological order; its total length equals `accumulatedMs`.
 */
export function accumulate(
  open: Interval[],
  from: number,
  durationMs: number,
  pauseDays: string[] = [],
): AccumulateResult {
  const effective = buildEffectiveCalendar(open, pauseDays);
  const direction: 1 | -1 = durationMs < 0 ? -1 : 1;
  const requestedMs = Math.abs(durationMs);
  let remaining = requestedMs;
  const evidence: Interval[] = [];

  if (direction === 1) {
    for (const iv of effective) {
      if (remaining === 0) break;
      if (iv.end <= from) continue;
      const start = Math.max(iv.start, from);
      const take = Math.min(remaining, iv.end - start);
      if (take > 0) {
        evidence.push({ start, end: start + take });
        remaining -= take;
      }
    }
  } else {
    for (let i = effective.length - 1; i >= 0 && remaining > 0; i--) {
      const iv = effective[i];
      if (iv.start >= from) continue;
      const end = Math.min(iv.end, from);
      const take = Math.min(remaining, end - iv.start);
      if (take > 0) {
        evidence.push({ start: end - take, end });
        remaining -= take;
      }
    }
    evidence.reverse();
  }

  const accumulatedMs = requestedMs - remaining;
  const end =
    evidence.length === 0
      ? from
      : direction === 1
        ? evidence[evidence.length - 1].end
        : evidence[0].start;

  return {
    from,
    end,
    direction,
    requestedMs,
    accumulatedMs,
    remainingMs: remaining,
    complete: remaining === 0,
    evidence,
  };
}
