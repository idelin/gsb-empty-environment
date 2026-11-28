/**
 * DST-safe conversions between local wall-clock components (interpreted in an
 * IANA time zone) and absolute UTC instants.
 *
 * The implementation relies exclusively on the runtime's full-ICU `Intl`
 * tzdata. We never assume a day is 86400 seconds: cadence math is performed in
 * wall components and re-resolved to an instant on every step, so spring
 * forward days become 23h and fall back days 25h automatically.
 */

import type { LocalDateTimeComponents, WallTime } from "./types.js";

const MS_PER_MINUTE = 60_000;
const MS_PER_HOUR = 3_600_000;
const MS_PER_DAY = 86_400_000;

/**
 * UTC offset in milliseconds for `instantMs` in `timeZone`
 * (e.g. New York winter => -5h, summer => -4h).
 */
export function offsetMsAt(timeZone: string, instantMs: number): number {
  // formatToParts with a long/short offset name is the portable way to read
  // the offset that ICU already computed for the instant.
  const dtf = new Intl.DateTimeFormat("en-US", {
    timeZone,
    timeZoneName: "longOffset",
    hour: "2-digit",
  });
  const parts = dtf.formatToParts(new Date(instantMs));
  const name = parts.find((p) => p.type === "timeZoneName")?.value ?? "";
  return parseOffsetName(name);
}

function parseOffsetName(name: string): number {
  // Forms: "GMT", "GMT-05:00", "GMT+05:30", "GMT+00:00".
  const match = /GMT([+-])(\d{2}):?(\d{2})?/.exec(name);
  if (!match) return 0;
  const sign = match[1] === "-" ? -1 : 1;
  const hours = Number(match[2]);
  const minutes = match[3] ? Number(match[3]) : 0;
  return sign * (hours * MS_PER_HOUR + minutes * MS_PER_MINUTE);
}

/** Convert absolute UTC instant -> wall components in `timeZone`. */
export function utcToZonedParts(
  timeZone: string,
  instantMs: number,
): Required<Omit<LocalDateTimeComponents, "year" | "month" | "day">> &
  Pick<LocalDateTimeComponents, "year" | "month" | "day"> {
  const dtf = new Intl.DateTimeFormat("en-US", {
    timeZone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
  const value: Record<string, number> = {};
  for (const part of dtf.formatToParts(new Date(instantMs))) {
    if (part.type !== "literal" && part.type !== "era") {
      value[part.type] = Number(part.value);
    }
  }
  // ICU hour12:false may report 24 for midnight in some builds; normalise.
  let hour = value.hour;
  let day = value.day;
  if (hour === 24) {
    hour = 0;
  }
  return {
    year: value.year,
    month: value.month,
    day,
    hour,
    minute: value.minute,
    second: value.second,
    millisecond: 0,
  };
}

export interface Disambiguation {
  /**
   * - "compatible" (default): gaps jump forward to the first valid instant;
   *   overlaps resolve to the earlier (pre-transition, longer-offset) instant.
   * - "earlier": force the earlier instant on overlaps (same as compatible).
   * - "later": force the later instant on overlaps.
   */
  disambiguation?: "compatible" | "earlier" | "later";
}

/**
 * Resolve local wall components to a UTC instant.
 *
 * Spring-forward gaps (a wall time that never exists) are shifted forward to
 * the first instant that exists. Fall-back overlaps (a wall time that exists
 * twice) resolve to the earlier instant by default, or the later one when
 * requested.
 */
export function zonedPartsToUtc(
  timeZone: string,
  parts: LocalDateTimeComponents,
  options: Disambiguation = {},
): number {
  const disambiguation = options.disambiguation ?? "compatible";
  const second = parts.second ?? 0;
  const millisecond = parts.millisecond ?? 0;
  const naiveUtc = Date.UTC(
    parts.year,
    parts.month - 1,
    parts.day,
    parts.hour,
    parts.minute,
    second,
    millisecond,
  );

  // Read the tzdata offset at the naive label, then build a first candidate by
  // removing that offset. Re-reading the offset at the candidate distinguishes
  // the three regimes (normal / spring gap / fall overlap).
  const offsetAtLabel = offsetMsAt(timeZone, naiveUtc);
  const candidate = naiveUtc - offsetAtLabel;
  const offsetAtCandidate = offsetMsAt(timeZone, candidate);

  if (offsetAtCandidate === offsetAtLabel) {
    // The candidate's wall time equals the requested wall time. This is a
    // normal instant, or the earlier occurrence of a fall-back overlap.
    if (disambiguation === "later") {
      const later = findLaterOverlapInstant(timeZone, naiveUtc, candidate);
      if (later !== null) return later;
    }
    return candidate;
  }

  // Offsets disagree: the requested wall time never exists (spring-forward
  // gap). `candidate` is already the first instant just after the jump, which
  // is the canonical forward shift (e.g. 02:30 -> 03:30 local).
  return candidate;
}

/** Convenience: wall time-of-day components -> UTC instant on a given local date. */
export function zonedTimeToUtc(
  timeZone: string,
  dateParts: Pick<LocalDateTimeComponents, "year" | "month" | "day">,
  wall: WallTime,
  options?: Disambiguation,
): number {
  return zonedPartsToUtc(
    timeZone,
    {
      year: dateParts.year,
      month: dateParts.month,
      day: dateParts.day,
      hour: wall.hour,
      minute: wall.minute,
      second: wall.second ?? 0,
      millisecond: wall.millisecond ?? 0,
    },
    options,
  );
}

/**
 * Add one cadence step in wall components and re-resolve to an instant. This is
 * the operation that produces 23h/25h handoff spacing across DST.
 */
export function advanceWallParts(
  timeZone: string,
  instantMs: number,
  steps: number,
  unit: "day" | "week" | "custom",
  customDurationMs?: number,
): number {
  if (unit === "custom") {
    // Custom shifts are defined as a fixed elapsed duration; they still go
    // through normal instants (no 86400 day assumption is involved).
    return instantMs + steps * (customDurationMs ?? MS_PER_DAY);
  }
  const parts = utcToZonedParts(timeZone, instantMs);
  const shifted = addWallDays(
    parts,
    steps * (unit === "week" ? 7 : 1),
  );
  return zonedPartsToUtc(timeZone, {
    year: shifted.year,
    month: shifted.month,
    day: shifted.day,
    hour: parts.hour,
    minute: parts.minute,
    second: parts.second,
    millisecond: parts.millisecond,
  });
}

/**
 * Add a whole number of days to wall-calendar components without interpreting
 * them as an instant. Calendar arithmetic (month/year rollover) is done in the
 * UTC-labelled frame purely for convenience; the returned value stays a set of
 * wall components and is later re-resolved against the target time zone.
 */
export function addWallDays(
  parts: Pick<
    LocalDateTimeComponents,
    "year" | "month" | "day"
  >,
  days: number,
): { year: number; month: number; day: number } {
  const labelled = Date.UTC(parts.year, parts.month - 1, parts.day + days);
  const date = new Date(labelled);
  return {
    year: date.getUTCFullYear(),
    month: date.getUTCMonth() + 1,
    day: date.getUTCDate(),
  };
}

/** Weekday (0=Sunday..6=Saturday) of an instant in `timeZone`. */
export function zonedWeekday(timeZone: string, instantMs: number): number {
  const parts = utcToZonedParts(timeZone, instantMs);
  return new Date(
    Date.UTC(parts.year, parts.month - 1, parts.day),
  ).getUTCDay();
}

function findLaterOverlapInstant(
  timeZone: string,
  naiveUtc: number,
  earlier: number,
): number | null {
  // During a fall-back overlap the same wall label has two valid instants.
  // Using the post-transition offset yields the later one. If that candidate
  // does not re-map to the requested wall label, there was no overlap.
  const postOffset = offsetMsAt(timeZone, earlier + MS_PER_HOUR);
  const later = naiveUtc - postOffset;
  if (later === earlier) return null;
  const laterWall = utcToZonedParts(timeZone, later);
  const laterLabel = Date.UTC(
    laterWall.year,
    laterWall.month - 1,
    laterWall.day,
    laterWall.hour,
    laterWall.minute,
    laterWall.second,
    laterWall.millisecond,
  );
  return laterLabel === naiveUtc ? later : null;
}
