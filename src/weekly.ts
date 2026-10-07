/**
 * Weekly recurrence over explicit integer date serials.
 *
 * A date serial is an integer day number; serial 0 is a Monday.
 * Weekdays are 0 (Monday) through 6 (Sunday).
 *
 * Semantics:
 * - `count` limits the number of candidate occurrences generated BEFORE
 *   exclusion dates are applied (RFC 5545 style): excluded dates shrink the
 *   series, they never extend it.
 * - Excluded dates remove occurrences without replacement.
 * - A moved (rescheduled) instance keeps its original instance id, which is
 *   derived from the rule id and the instance's ORIGINAL serial.
 */

export interface WeeklyRule {
  /** Stable rule identifier, embedded into instance ids. */
  id: string;
  /** First serial eligible for an occurrence (inclusive). */
  startSerial: number;
  /** Week interval: 1 = every week, 2 = every other week, ... */
  intervalWeeks: number;
  /** Weekdays on which occurrences fall; 0 = Monday .. 6 = Sunday. */
  weekdays: number[];
  /** Number of candidate occurrences counted BEFORE exclusions. */
  count: number;
  /** Serials removed from the series without replacement. */
  exdates?: number[];
}

export interface MoveOverride {
  /** Original serial of the occurrence being rescheduled. */
  originalSerial: number;
  /** Serial the occurrence is moved to. */
  newSerial: number;
}

export interface Instance {
  /** Stable id: `<ruleId>@<originalSerial>`; survives rescheduling. */
  id: string;
  /** Serial on which the instance currently falls. */
  serial: number;
  /** Serial on which the recurrence originally placed the instance. */
  originalSerial: number;
  /** True when the instance was rescheduled away from its original serial. */
  moved: boolean;
}

export interface Page {
  instances: Instance[];
  offset: number;
  limit: number;
  /** Total number of instances across all pages. */
  total: number;
  /** True when more instances exist beyond this page. */
  hasMore: boolean;
}

const DAYS_PER_WEEK = 7;

export function weekdayOf(serial: number): number {
  return ((serial % DAYS_PER_WEEK) + DAYS_PER_WEEK) % DAYS_PER_WEEK;
}

/** Serial of the Monday of the week containing `serial`. */
export function weekStartOf(serial: number): number {
  return serial - weekdayOf(serial);
}

export function instanceId(ruleId: string, originalSerial: number): string {
  return `${ruleId}@${originalSerial}`;
}

function validateRule(rule: WeeklyRule): void {
  if (!Number.isInteger(rule.startSerial)) {
    throw new RangeError("startSerial must be an integer");
  }
  if (!Number.isInteger(rule.intervalWeeks) || rule.intervalWeeks < 1) {
    throw new RangeError("intervalWeeks must be an integer >= 1");
  }
  if (!Number.isInteger(rule.count) || rule.count < 0) {
    throw new RangeError("count must be an integer >= 0");
  }
  if (rule.weekdays.length === 0) {
    throw new RangeError("weekdays must not be empty");
  }
  for (const day of rule.weekdays) {
    if (!Number.isInteger(day) || day < 0 || day > 6) {
      throw new RangeError(`weekday out of range: ${day}`);
    }
  }
}

/**
 * Pre-exclusion candidate serials in ascending order, exactly `rule.count`
 * of them. Exclusions are NOT applied here.
 */
export function expandCandidates(rule: WeeklyRule): number[] {
  validateRule(rule);
  const days = [...new Set(rule.weekdays)].sort((a, b) => a - b);
  const candidates: number[] = [];
  const firstWeekStart = weekStartOf(rule.startSerial);
  for (
    let weekStart = firstWeekStart;
    candidates.length < rule.count;
    weekStart += rule.intervalWeeks * DAYS_PER_WEEK
  ) {
    for (const day of days) {
      const serial = weekStart + day;
      if (serial < rule.startSerial) continue;
      candidates.push(serial);
      if (candidates.length === rule.count) break;
    }
  }
  return candidates;
}

/**
 * Full expansion: candidates minus exdates, with move overrides applied.
 * Instances are sorted by current serial (ties broken by original serial,
 * so a moved instance and a natural instance can share one serial).
 */
export function expand(rule: WeeklyRule, moves: MoveOverride[] = []): Instance[] {
  const excluded = new Set(rule.exdates ?? []);
  const moveByOriginal = new Map<number, number>();
  for (const move of moves) {
    moveByOriginal.set(move.originalSerial, move.newSerial);
  }
  const instances: Instance[] = [];
  for (const originalSerial of expandCandidates(rule)) {
    if (excluded.has(originalSerial)) continue;
    const newSerial = moveByOriginal.get(originalSerial);
    const moved = newSerial !== undefined && newSerial !== originalSerial;
    instances.push({
      id: instanceId(rule.id, originalSerial),
      serial: moved ? newSerial : originalSerial,
      originalSerial,
      moved,
    });
  }
  instances.sort((a, b) => a.serial - b.serial || a.originalSerial - b.originalSerial);
  return instances;
}

/** Paginated expansion over the fully expanded, sorted instance list. */
export function expandPage(
  rule: WeeklyRule,
  options: { offset?: number; limit: number; moves?: MoveOverride[] },
): Page {
  const { limit } = options;
  const offset = options.offset ?? 0;
  if (!Number.isInteger(limit) || limit < 1) {
    throw new RangeError("limit must be an integer >= 1");
  }
  if (!Number.isInteger(offset) || offset < 0) {
    throw new RangeError("offset must be an integer >= 0");
  }
  const all = expand(rule, options.moves ?? []);
  return {
    instances: all.slice(offset, offset + limit),
    offset,
    limit,
    total: all.length,
    hasMore: offset + limit < all.length,
  };
}

/** Instances whose current serial falls within [fromSerial, toSerial]. */
export function expandRange(
  rule: WeeklyRule,
  fromSerial: number,
  toSerial: number,
  moves: MoveOverride[] = [],
): Instance[] {
  return expand(rule, moves).filter(
    (instance) => instance.serial >= fromSerial && instance.serial <= toSerial,
  );
}

/** All instances landing on a single serial (basis for day-by-day scans). */
export function instancesOn(
  rule: WeeklyRule,
  serial: number,
  moves: MoveOverride[] = [],
): Instance[] {
  return expand(rule, moves).filter((instance) => instance.serial === serial);
}

/** Serial of the last pre-exclusion candidate, or null when count is 0. */
export function lastCandidateSerial(rule: WeeklyRule): number | null {
  const candidates = expandCandidates(rule);
  return candidates.length === 0 ? null : candidates[candidates.length - 1];
}
