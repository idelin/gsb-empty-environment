import type { HalfOpenInterval, Timestamp } from "./types.ts";

export function interval(from: Timestamp, to: Timestamp): HalfOpenInterval {
  return { from, to };
}

export function isEmpty(value: HalfOpenInterval): boolean {
  return value.to <= value.from;
}

export function intersect(
  a: HalfOpenInterval,
  b: HalfOpenInterval,
): HalfOpenInterval {
  return {
    from: Math.max(a.from, b.from),
    to: Math.min(a.to, b.to),
  };
}

export function overlaps(a: HalfOpenInterval, b: HalfOpenInterval): boolean {
  return !isEmpty(intersect(a, b));
}

export function containsAt(value: HalfOpenInterval, at: Timestamp): boolean {
  return value.from <= at && at < value.to;
}

/**
 * Subtract half-open interval `cut` from `base`, returning the (up to two)
 * surviving half-open pieces. Selective time-windowed revocation uses this so
 * that cutting one window leaves the surrounding consent intact.
 */
export function subtract(
  base: HalfOpenInterval,
  cut: HalfOpenInterval,
): HalfOpenInterval[] {
  const shared = intersect(base, cut);
  if (isEmpty(shared)) {
    return [base];
  }
  const pieces: HalfOpenInterval[] = [];
  if (base.from < shared.from) {
    pieces.push({ from: base.from, to: shared.from });
  }
  if (shared.to < base.to) {
    pieces.push({ from: shared.to, to: base.to });
  }
  return pieces;
}

/**
 * Remove every interval in `cuts` from the input list. Cuts may overlap each
 * other; the result is the exact surviving set (no double subtraction).
 */
export function subtractAll(
  bases: readonly HalfOpenInterval[],
  cuts: readonly HalfOpenInterval[],
): HalfOpenInterval[] {
  let surviving = bases.filter((value) => !isEmpty(value));
  for (const cut of cuts) {
    const next: HalfOpenInterval[] = [];
    for (const piece of surviving) {
      next.push(...subtract(piece, cut));
    }
    surviving = next;
  }
  return surviving;
}
