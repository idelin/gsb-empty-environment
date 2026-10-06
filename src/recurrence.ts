/**
 * Weekly recurrence over explicit integer date ordinals.
 *
 * An ordinal is a whole-day serial number (any integer, negative allowed).
 * The weekday of an ordinal is `ordinal mod 7` (ordinal 0 is weekday 0).
 * Weeks are aligned to the week containing `rule.start`.
 */

export interface WeeklyRule {
  /** First eligible day (inclusive), as an integer ordinal. */
  start: number;
  /** Week interval: every `interval`-th week is active. Must be >= 1. */
  interval: number;
  /** Weekdays (0..6) on which occurrences land within an active week. */
  weekdays: number[];
  /**
   * Termination: total number of candidate occurrences, counted BEFORE
   * exclusions. Excluded candidates still consume the count, so exdates
   * shrink the visible series instead of extending it.
   */
  count?: number;
  /** Ordinals removed from the visible series. */
  exdates?: number[];
}

export interface Instance {
  /** Stable id, derived from the original occurrence date. */
  id: string;
  /** Current date (differs from originalDate after a reschedule). */
  date: number;
  /** Date of the underlying recurrence candidate. */
  originalDate: number;
}

export function mod(n: number, m: number): number {
  return ((n % m) + m) % m;
}

export function weekdayOf(ordinal: number): number {
  return mod(ordinal, 7);
}

export function weekStartOf(ordinal: number): number {
  return ordinal - weekdayOf(ordinal);
}

export function instanceId(originalDate: number): string {
  return `inst@${originalDate}`;
}

export function validateRule(rule: WeeklyRule): void {
  if (!Number.isInteger(rule.start)) throw new Error("start must be an integer ordinal");
  if (!Number.isInteger(rule.interval) || rule.interval < 1) {
    throw new Error("interval must be an integer >= 1");
  }
  if (!Array.isArray(rule.weekdays) || rule.weekdays.length === 0) {
    throw new Error("weekdays must be a non-empty array");
  }
  for (const wd of rule.weekdays) {
    if (!Number.isInteger(wd) || wd < 0 || wd > 6) {
      throw new Error(`weekday out of range: ${wd}`);
    }
  }
  if (rule.count !== undefined && (!Number.isInteger(rule.count) || rule.count < 0)) {
    throw new Error("count must be an integer >= 0");
  }
  for (const ex of rule.exdates ?? []) {
    if (!Number.isInteger(ex)) throw new Error(`exdate must be an integer ordinal: ${ex}`);
  }
}

/**
 * Yields candidate ordinals in ascending order, BEFORE applying count or
 * exdates. Infinite generator: callers must stop based on count/window.
 */
export function* generateCandidates(rule: WeeklyRule): Generator<number, void, void> {
  const weekdays = Array.from(new Set(rule.weekdays)).sort((a, b) => a - b);
  const startWeek = weekStartOf(rule.start);
  let weekIndex = 0;
  for (let week = startWeek; ; week += 7, weekIndex++) {
    if (weekIndex % rule.interval !== 0) continue;
    for (const wd of weekdays) {
      const day = week + wd;
      if (day >= rule.start) yield day;
    }
  }
}

/**
 * Visible occurrence dates in [from, to): candidates with the count applied
 * (pre-exclusion) and exdates removed. `to` must be finite.
 */
export function* generateDates(rule: WeeklyRule, from: number, to: number): Generator<number, void, void> {
  const exdates = new Set(rule.exdates ?? []);
  let seen = 0;
  for (const day of generateCandidates(rule)) {
    if (day >= to) break;
    seen++;
    if (rule.count !== undefined && seen > rule.count) break;
    if (exdates.has(day)) continue;
    if (day >= from) yield day;
  }
}

/** True when `originalDate` is a visible occurrence of the rule. */
export function isOccurrence(rule: WeeklyRule, originalDate: number): boolean {
  const exdates = new Set(rule.exdates ?? []);
  if (exdates.has(originalDate)) return false;
  let seen = 0;
  for (const day of generateCandidates(rule)) {
    if (day > originalDate) return false;
    seen++;
    if (rule.count !== undefined && seen > rule.count) return false;
    if (day === originalDate) return true;
  }
  return false;
}
