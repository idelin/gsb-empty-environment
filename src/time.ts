import { Rational, rat } from "./rational.ts";
import type { RationalInput } from "./rational.ts";

export interface RationalRangeInput {
  lo: RationalInput;
  hi: RationalInput;
  loInclusive?: boolean;
  hiInclusive?: boolean;
}

export interface RationalRange {
  lo: Rational;
  hi: Rational;
  loInclusive: boolean;
  hiInclusive: boolean;
}

/** A marker may be unbounded.  A null lower marker means -infinity and a
 * null upper marker means +infinity.  The marker's `inclusive` flag describes
 * whether the named boundary itself belongs to the set. */
export interface TimeMarker {
  time: Rational | null;
  inclusive: boolean;
  /** Required when time is null to distinguish -infinity from +infinity. */
  infinite?: "negative" | "positive";
}

export interface LeaseTermInput {
  start: RationalInput;
  end: RationalInput;
  startInclusive?: boolean;
  endInclusive?: boolean;
}

export interface LeaseTerm {
  start: Rational;
  end: Rational;
  startInclusive: boolean;
  endInclusive: boolean;
}

export interface ValidityWindow {
  possibleFrom: TimeMarker;
  possibleUntil: TimeMarker;
  definiteFrom: TimeMarker | null;
  definiteUntil: TimeMarker | null;
}

export function rationalRange(input: RationalRangeInput): RationalRange {
  const lo = rat(input.lo);
  const hi = rat(input.hi);
  if (lo.gt(hi)) throw new RangeError(`rational range has lo > hi: ${lo} > ${hi}`);
  const loInclusive = input.loInclusive ?? true;
  const hiInclusive = input.hiInclusive ?? true;
  if (lo.eq(hi) && (!loInclusive || !hiInclusive)) {
    return { lo, hi, loInclusive, hiInclusive };
  }
  return { lo, hi, loInclusive, hiInclusive };
}

export function leaseTerm(input: LeaseTermInput): LeaseTerm {
  const start = rat(input.start);
  const end = rat(input.end);
  if (start.gt(end)) throw new RangeError(`lease term has start > end: ${start} > ${end}`);
  return {
    start,
    end,
    startInclusive: input.startInclusive ?? true,
    endInclusive: input.endInclusive ?? false,
  };
}

export function unboundedLower(inclusive = false): TimeMarker {
  return { time: null, inclusive, infinite: "negative" };
}

export function unboundedUpper(inclusive = false): TimeMarker {
  return { time: null, inclusive, infinite: "positive" };
}

export function markerCmp(a: TimeMarker, b: TimeMarker, lower: boolean): number {
  if (a.time === null && b.time === null) {
    if (a.infinite === b.infinite) return 0;
    return a.infinite === "negative" ? -1 : 1;
  }
  if (a.time === null) return a.infinite === "negative" ? -1 : 1;
  if (b.time === null) return b.infinite === "negative" ? 1 : -1;
  const cmp = a.time.cmp(b.time);
  if (cmp !== 0) return cmp;
  return 0;
}

export function markerTimeString(marker: TimeMarker, side: "lower" | "upper"): string {
  if (marker.time === null) {
    return marker.infinite === "positive" ? "+infinity" : "-infinity";
  }
  const bracket = marker.inclusive ? "[" : "(";
  return `${bracket}${marker.time.toString()}`;
}

export function compareMarkers(a: TimeMarker, b: TimeMarker): number {
  if (a.time === null && b.time === null) {
    if (a.infinite === b.infinite) return 0;
    return a.infinite === "negative" ? -1 : 1;
  }
  if (a.time === null) return a.infinite === "negative" ? -1 : 1;
  if (b.time === null) return b.infinite === "negative" ? 1 : -1;
  return a.time.cmp(b.time);
}

export function maxMarker(a: TimeMarker, b: TimeMarker): TimeMarker {
  const cmp = compareMarkers(a, b);
  if (cmp > 0) return a;
  if (cmp < 0) return b;
  return {
    time: a.time,
    inclusive: a.inclusive && b.inclusive,
    ...(a.time === null ? { infinite: a.infinite } : {}),
  };
}

export function minMarker(a: TimeMarker, b: TimeMarker): TimeMarker {
  const cmp = compareMarkers(a, b);
  if (cmp < 0) return a;
  if (cmp > 0) return b;
  return {
    time: a.time,
    inclusive: a.inclusive && b.inclusive,
    ...(a.time === null ? { infinite: a.infinite } : {}),
  };
}

export function markerIsEmptyLowerUpper(lo: TimeMarker, hi: TimeMarker): boolean {
  if (lo.time === null && hi.time === null) {
    return lo.infinite === "positive" && hi.infinite === "negative";
  }
  if (lo.time === null) return lo.infinite === "positive";
  if (hi.time === null) return hi.infinite === "negative";
  const cmp = lo.time.cmp(hi.time);
  if (cmp > 0) return true;
  if (cmp < 0) return false;
  return !(lo.inclusive && hi.inclusive);
}

export function markerContains(marker: TimeMarker, value: RationalInput, lower: boolean): boolean {
  const t = rat(value);
  if (marker.time === null) {
    return marker.infinite === "negative" ? lower : !lower;
  }
  const cmp = t.cmp(marker.time);
  if (lower) {
    return cmp > 0 || (cmp === 0 && marker.inclusive);
  }
  return cmp < 0 || (cmp === 0 && marker.inclusive);
}

export function emptyWindow(): ValidityWindow {
  return {
    possibleFrom: { time: Rational.zero(), inclusive: false },
    possibleUntil: { time: Rational.zero(), inclusive: false },
    definiteFrom: null,
    definiteUntil: null,
  };
}

export function windowIsEmpty(window: ValidityWindow): boolean {
  // A window may be possibly non-empty while its definite sub-window is empty
  // (large clock uncertainty).  Only the possible bounds decide existence.
  return markerIsEmptyLowerUpper(window.possibleFrom, window.possibleUntil);
}

export function windowPossibleAt(window: ValidityWindow, time: RationalInput): boolean {
  if (windowIsEmpty(window)) return false;
  return (
    markerContains(window.possibleFrom, time, true) &&
    markerContains(window.possibleUntil, time, false)
  );
}

export function windowDefiniteAt(window: ValidityWindow, time: RationalInput): boolean {
  if (window.definiteFrom === null || window.definiteUntil === null) return false;
  if (markerIsEmptyLowerUpper(window.definiteFrom, window.definiteUntil)) return false;
  return (
    markerContains(window.definiteFrom, time, true) &&
    markerContains(window.definiteUntil, time, false)
  );
}

export function intersectWindows(a: ValidityWindow, b: ValidityWindow): ValidityWindow {
  if (windowIsEmpty(a) || windowIsEmpty(b)) return emptyWindow();
  const possibleFrom = maxMarker(a.possibleFrom, b.possibleFrom);
  const possibleUntil = minMarker(a.possibleUntil, b.possibleUntil);

  let definiteFrom: TimeMarker | null = null;
  let definiteUntil: TimeMarker | null = null;
  if (a.definiteFrom !== null && b.definiteFrom !== null) {
    definiteFrom = maxMarker(a.definiteFrom, b.definiteFrom);
  } else if (a.definiteFrom !== null || b.definiteFrom !== null) {
    // A conjunction is only definite when both components are definite.
    definiteFrom = null;
  }
  if (a.definiteUntil !== null && b.definiteUntil !== null) {
    definiteUntil = minMarker(a.definiteUntil, b.definiteUntil);
  } else if (a.definiteUntil !== null || b.definiteUntil !== null) {
    definiteUntil = null;
  }

  const result: ValidityWindow = {
    possibleFrom,
    possibleUntil,
    definiteFrom,
    definiteUntil,
  };
  if (markerIsEmptyLowerUpper(possibleFrom, possibleUntil)) return emptyWindow();
  if (
    definiteFrom !== null &&
    definiteUntil !== null &&
    markerIsEmptyLowerUpper(definiteFrom, definiteUntil)
  ) {
    result.definiteFrom = null;
    result.definiteUntil = null;
  }
  return result;
}

/** The lower boundary from which no member of the possible window can still
 * be present.  An inclusive possible end T means safe re-grant is strictly
 * after T; an exclusive end means T itself is already safe. */
export function safeAfterWindow(window: ValidityWindow): TimeMarker {
  if (windowIsEmpty(window)) return unboundedLower(true);
  if (window.possibleUntil.time === null) {
    return window.possibleUntil.infinite === "positive"
      ? unboundedUpper(false)
      : unboundedLower(false);
  }
  return {
    time: window.possibleUntil.time,
    inclusive: !window.possibleUntil.inclusive,
  };
}

export function markerToString(marker: TimeMarker, side: "lower" | "upper"): string {
  if (marker.time === null) {
    return marker.infinite === "positive" ? "+infinity" : "-infinity";
  }
  if (side === "lower") {
    return `${marker.inclusive ? ">=" : ">"} ${marker.time}`;
  }
  return `${marker.inclusive ? "<=" : "<"} ${marker.time}`;
}
