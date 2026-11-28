/**
 * DST-aware wall-clock arithmetic over IANA time zones.
 *
 * All instants are epoch milliseconds. Wall-clock components are always
 * interpreted in the supplied IANA zone, never against the host zone. No
 * assumption of a fixed 24h day is made anywhere: adding "one calendar day"
 * yields a 23h, 24h or 25h real interval depending on the zone's DST rules.
 */

export type Instant = number;

export interface WallClock {
  year: number;
  month: number;
  day: number;
  hour: number;
  minute: number;
  second: number;
  millisecond: number;
}

/** How to resolve a wall time that is repeated during a fall-back overlap. */
export type Disambiguation = "earlier" | "later" | "compatible";

const DAY_MS = 86_400_000;
const HOUR_MS = 3_600_000;
const MINUTE_MS = 60_000;

const offsetFormatters = new Map<string, Intl.DateTimeFormat>();
const partFormatters = new Map<string, Intl.DateTimeFormat>();

function offsetFormatter(timeZone: string): Intl.DateTimeFormat {
  let formatter = offsetFormatters.get(timeZone);
  if (formatter === undefined) {
    formatter = new Intl.DateTimeFormat("en-US", {
      timeZone,
      timeZoneName: "longOffset",
      hour12: false,
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
    offsetFormatters.set(timeZone, formatter);
  }
  return formatter;
}

function partFormatter(timeZone: string): Intl.DateTimeFormat {
  let formatter = partFormatters.get(timeZone);
  if (formatter === undefined) {
    formatter = new Intl.DateTimeFormat("en-US", {
      timeZone,
      hour12: false,
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
    partFormatters.set(timeZone, formatter);
  }
  return formatter;
}

/** UTC offset of `instant` as observed inside `timeZone`, in milliseconds. */
export function utcOffset(timeZone: string, instant: Instant): number {
  const parts = offsetFormatter(timeZone).formatToParts(new Date(instant));
  const token = parts.find((part) => part.type === "timeZoneName")?.value ?? "";
  const gmtIndex = token.indexOf("GMT");
  if (gmtIndex === -1) {
    return 0;
  }
  const body = token.slice(gmtIndex + 3);
  if (body === "" || body === "Z" || body === "UTC") {
    return 0;
  }
  const sign = body.startsWith("-") ? -1 : 1;
  const segments = body.replace(/^[+-]/, "").split(":");
  const hours = Number(segments[0] ?? 0);
  const minutes = Number(segments[1] ?? 0);
  const seconds = Number(segments[2] ?? 0);
  return sign * (hours * HOUR_MS + minutes * MINUTE_MS + seconds * 1000);
}

/** Local wall-clock components of `instant` inside `timeZone`. */
export function wallPartsAt(timeZone: string, instant: Instant): WallClock {
  const lookup = new Map<string, string>();
  for (const part of partFormatter(timeZone).formatToParts(new Date(instant))) {
    if (part.type !== "literal") {
      lookup.set(part.type, part.value);
    }
  }
  const hour = Number(lookup.get("hour") ?? "0");
  return {
    year: Number(lookup.get("year") ?? "0"),
    month: Number(lookup.get("month") ?? "1"),
    day: Number(lookup.get("day") ?? "1"),
    hour: hour === 24 ? 0 : hour,
    minute: Number(lookup.get("minute") ?? "0"),
    second: Number(lookup.get("second") ?? "0"),
    millisecond: 0,
  };
}

/** Pseudo-UTC epoch ms of wall components: the same Y/M/D/h/m/s read as UTC. */
export function wallToPseudoUtc(wall: WallClock): Instant {
  return Date.UTC(
    wall.year,
    wall.month - 1,
    wall.day,
    wall.hour,
    wall.minute,
    wall.second,
    wall.millisecond,
  );
}

function pseudoUtcToWall(pseudo: Instant): WallClock {
  const date = new Date(pseudo);
  return {
    year: date.getUTCFullYear(),
    month: date.getUTCMonth() + 1,
    day: date.getUTCDate(),
    hour: date.getUTCHours(),
    minute: date.getUTCMinutes(),
    second: date.getUTCSeconds(),
    millisecond: date.getUTCMilliseconds(),
  };
}

/** Offset transition instants strictly inside the (lo, hi) window. */
function findTransitions(timeZone: string, lo: Instant, hi: Instant): Instant[] {
  const transitions: Instant[] = [];

  const walk = (left: Instant, right: Instant): void => {
    if (right - left <= MINUTE_MS) {
      if (utcOffset(timeZone, left) !== utcOffset(timeZone, right)) {
        transitions.push(right);
      }
      return;
    }
    const leftOffset = utcOffset(timeZone, left);
    const rightOffset = utcOffset(timeZone, right);
    if (rightOffset === leftOffset) {
      return;
    }
    const mid = Math.floor((left + right) / 2 / 60_000) * 60_000;
    if (mid <= left) {
      transitions.push(right);
      return;
    }
    walk(left, mid);
    walk(mid, right);
  };

  walk(lo, hi);
  return transitions.sort((a, b) => a - b);
}

export interface ResolvedWall {
  instant: Instant;
  /** True when the requested wall time fell inside a spring-forward gap. */
  skipped: boolean;
  /** True when the requested wall time is ambiguous due to a fall-back. */
  ambiguous: boolean;
  offset: number;
}

/**
 * Resolve a local wall time to a concrete UTC instant.
 *
 * Spring-forward gaps are clamped to the first instant after the gap; both
 * `earlier` and `later` converge there because no real instant carries the
 * skipped wall time. Fall-back overlaps return the first or second occurrence
 * according to `disambiguation` (the default `compatible` means `earlier`).
 */
export function resolveWall(
  timeZone: string,
  wall: WallClock,
  disambiguation: Disambiguation = "compatible",
): ResolvedWall {
  const pseudo = wallToPseudoUtc(wall);
  const transitions = findTransitions(timeZone, pseudo - 26 * HOUR_MS, pseudo + 26 * HOUR_MS);

  for (let index = 0; index < transitions.length; index += 1) {
    const transition = transitions[index];
    const beforeOffset = utcOffset(timeZone, transition - MINUTE_MS);
    const afterOffset = utcOffset(timeZone, transition + MINUTE_MS);
    const delta = afterOffset - beforeOffset;

    if (delta > 0) {
      // Spring forward: wall times [transition-before, transition-after) do not exist.
      const gapStartPseudo = transition + beforeOffset;
      const gapEndPseudo = transition + afterOffset;
      if (pseudo >= gapStartPseudo && pseudo < gapEndPseudo) {
        return { instant: transition, skipped: true, ambiguous: false, offset: afterOffset };
      }
    } else if (delta < 0) {
      // Fall back: wall times [transition-after, transition-before) occur twice.
      const overlapStartPseudo = transition + afterOffset;
      const overlapEndPseudo = transition + beforeOffset;
      if (pseudo >= overlapStartPseudo && pseudo < overlapEndPseudo) {
        const earlier = pseudo - beforeOffset;
        const later = pseudo - afterOffset;
        const useLater = disambiguation === "later";
        return {
          instant: useLater ? later : earlier,
          skipped: false,
          ambiguous: true,
          offset: useLater ? beforeOffset : afterOffset,
        };
      }
    }
  }

  // `pseudo` encodes the wall components read as if they were UTC. Reading the
  // offset at the pseudo instant is sufficient: outside a gap/overlap the
  // pseudo and the real instant share an offset, so
  //   instant = pseudo - offset(pseudo).
  const instant = pseudo - utcOffset(timeZone, pseudo);
  return {
    instant,
    skipped: false,
    ambiguous: false,
    offset: utcOffset(timeZone, instant),
  };
}

export interface WallDuration {
  days?: number;
  hours?: number;
  minutes?: number;
  seconds?: number;
  milliseconds?: number;
}

/**
 * Add calendar-denominated wall time inside `timeZone`.
 *
 * The arithmetic is performed on wall components first and only then resolved
 * back to UTC, so crossing a DST transition preserves the requested wall time
 * (e.g. 09:00 + 1 day stays 09:00 local even though the real elapsed time is
 * 23h or 25h on a transition day).
 */
export function addWallDuration(
  timeZone: string,
  instant: Instant,
  duration: WallDuration,
  disambiguation: Disambiguation = "compatible",
): Instant {
  const wall = wallPartsAt(timeZone, instant);
  const shiftedPseudo =
    wallToPseudoUtc(wall) +
    (duration.days ?? 0) * DAY_MS +
    (duration.hours ?? 0) * HOUR_MS +
    (duration.minutes ?? 0) * MINUTE_MS +
    (duration.seconds ?? 0) * 1000 +
    (duration.milliseconds ?? 0);
  return resolveWall(timeZone, pseudoUtcToWall(shiftedPseudo), disambiguation).instant;
}

/** Day of week (0 = Sunday ... 6 = Saturday) of an instant inside `timeZone`. */
export function localWeekday(timeZone: string, instant: Instant): number {
  const wall = wallPartsAt(timeZone, instant);
  return new Date(Date.UTC(wall.year, wall.month - 1, wall.day)).getUTCDay();
}

export function toInstant(iso: string): Instant {
  return Date.parse(iso);
}

export function toIso(instant: Instant): string {
  return new Date(instant).toISOString();
}
