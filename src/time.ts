import type { Instant, TimeInterval } from "./types.ts";

export function toEpochMs(value: Instant): number {
  if (value instanceof Date) {
    return value.getTime();
  }
  if (typeof value === "number") {
    return value;
  }
  const parsed = Date.parse(value);
  if (Number.isNaN(parsed)) {
    throw new TypeError(`Invalid instant: ${String(value)}`);
  }
  return parsed;
}

export function normalizeInterval(interval: TimeInterval): Required<Pick<TimeInterval, "startInclusive" | "endInclusive">> & {
  start?: number;
  end?: number;
} {
  return {
    start: interval.start === undefined ? undefined : toEpochMs(interval.start),
    end: interval.end === undefined ? undefined : toEpochMs(interval.end),
    startInclusive: interval.startInclusive ?? true,
    endInclusive: interval.endInclusive ?? false,
  };
}

export function intervalContains(interval: TimeInterval | undefined, at: Instant): boolean {
  if (!interval) {
    return true;
  }
  const normalized = normalizeInterval(interval);
  const instant = toEpochMs(at);
  if (
    normalized.start !== undefined &&
    (instant < normalized.start ||
      (instant === normalized.start && !normalized.startInclusive))
  ) {
    return false;
  }
  if (
    normalized.end !== undefined &&
    (instant > normalized.end ||
      (instant === normalized.end && !normalized.endInclusive))
  ) {
    return false;
  }
  return true;
}

export function isEmptyInterval(interval: TimeInterval): boolean {
  const normalized = normalizeInterval(interval);
  if (normalized.start === undefined || normalized.end === undefined) {
    return false;
  }
  if (normalized.start > normalized.end) {
    return true;
  }
  if (normalized.start === normalized.end) {
    return !(normalized.startInclusive && normalized.endInclusive);
  }
  return false;
}

export function intersectIntervals(
  left: TimeInterval | undefined,
  right: TimeInterval | undefined,
): TimeInterval | undefined {
  if (!left) {
    return right;
  }
  if (!right) {
    return left;
  }
  const a = normalizeInterval(left);
  const b = normalizeInterval(right);
  let start = a.start;
  let startInclusive = a.startInclusive;
  if (b.start !== undefined && (start === undefined || b.start > start)) {
    start = b.start;
    startInclusive = b.startInclusive;
  } else if (b.start !== undefined && b.start === start) {
    startInclusive = a.startInclusive && b.startInclusive;
  }

  let end = a.end;
  let endInclusive = a.endInclusive;
  if (b.end !== undefined && (end === undefined || b.end < end)) {
    end = b.end;
    endInclusive = b.endInclusive;
  } else if (b.end !== undefined && b.end === end) {
    endInclusive = a.endInclusive && b.endInclusive;
  }

  const result: TimeInterval = { start, end, startInclusive, endInclusive };
  return isEmptyInterval(result) ? undefined : result;
}

/** A conventional waiver lifetime: start inclusive, expiry exclusive. */
export function validBetween(issuedAt: Instant, expiresAt: Instant): TimeInterval {
  return {
    start: issuedAt,
    end: expiresAt,
    startInclusive: true,
    endInclusive: false,
  };
}

