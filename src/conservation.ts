import { stackSeries } from './stack.ts';
import type {
  SeriesInput,
  StackedColumn,
  StackMode,
  StackResult,
} from './types.ts';

export const EPS = 1e-9;

export function approxEqual(a: number, b: number, eps: number = EPS): boolean {
  return Math.abs(a - b) <= eps * Math.max(1, Math.abs(a), Math.abs(b));
}

/** Per-series layer height (upper - lower) for one column. */
export function segmentHeights(column: StackedColumn): Map<string, number> {
  const heights = new Map<string, number>();
  for (const point of column.points) {
    heights.set(point.seriesId, point.upper - point.lower);
  }
  return heights;
}

/**
 * A column is all-zero when every layer (zero-valued or missing) sits at the
 * baseline and neither cursor moved.
 */
export function checkAllZeroColumn(column: StackedColumn): boolean {
  return (
    column.posExtent === 0 &&
    column.negExtent === 0 &&
    column.points.every((p) => p.lower === 0 && p.upper === 0)
  );
}

export interface OrderInvarianceReport {
  ok: boolean;
  violations: string[];
}

/**
 * Stacking is order-dependent per layer but order-invariant in aggregate:
 * for every column the positive extent, the negative extent and the multiset
 * of layer heights must not depend on the series order.
 */
export function checkOrderInvariance(
  a: StackResult,
  b: StackResult,
): OrderInvarianceReport {
  const violations: string[] = [];
  if (a.mode !== b.mode) {
    violations.push(`mode mismatch: ${a.mode} vs ${b.mode}`);
    return { ok: false, violations };
  }
  if (a.times.length !== b.times.length || a.times.some((t, i) => t !== b.times[i])) {
    violations.push('time alignment differs');
    return { ok: false, violations };
  }
  for (let i = 0; i < a.columns.length; i += 1) {
    const ca = a.columns[i];
    const cb = b.columns[i];
    const label = `column ${String(ca.time)}`;
    if (!approxEqual(ca.posExtent, cb.posExtent)) {
      violations.push(`${label}: positive extent ${ca.posExtent} != ${cb.posExtent}`);
    }
    if (!approxEqual(ca.negExtent, cb.negExtent)) {
      violations.push(`${label}: negative extent ${ca.negExtent} != ${cb.negExtent}`);
    }
    const ha = ca.points.map((p) => p.upper - p.lower).sort((x, y) => x - y);
    const hb = cb.points.map((p) => p.upper - p.lower).sort((x, y) => x - y);
    if (ha.length !== hb.length || ha.some((h, j) => !approxEqual(h, hb[j]))) {
      violations.push(`${label}: layer-height multiset differs`);
    }
  }
  return { ok: violations.length === 0, violations };
}

/**
 * Percent-mode conservation: within each column the positive layers sum to 1
 * when the positive total is non-zero (else 0), and likewise for the
 * negative layers. Returns a list of violation messages (empty when sound).
 */
export function checkPercentConservation(result: StackResult): string[] {
  const violations: string[] = [];
  if (result.mode !== 'percent') return violations;
  for (const column of result.columns) {
    let posSum = 0;
    let negSum = 0;
    for (const point of column.points) {
      if (point.missing) continue;
      const height = point.upper - point.lower;
      if (point.value! > 0) posSum += height;
      else if (point.value! < 0) negSum += height;
    }
    const label = `column ${String(column.time)}`;
    const posExpected = column.posTotal > 0 ? 1 : 0;
    const negExpected = column.negTotal > 0 ? 1 : 0;
    if (!approxEqual(posSum, posExpected)) {
      violations.push(`${label}: positive layers sum to ${posSum}, expected ${posExpected}`);
    }
    if (!approxEqual(negSum, negExpected)) {
      violations.push(`${label}: negative layers sum to ${negSum}, expected ${negExpected}`);
    }
    if (!approxEqual(column.posExtent, posSum)) {
      violations.push(`${label}: positive extent ${column.posExtent} != layer sum ${posSum}`);
    }
    if (!approxEqual(column.negExtent, -negSum)) {
      violations.push(`${label}: negative extent ${column.negExtent} != -layer sum ${-negSum}`);
    }
  }
  return violations;
}

/** Plain-object projection of a StackResult for structural comparison. */
export function normalizeResult(result: StackResult): unknown {
  return {
    mode: result.mode,
    times: result.times,
    seriesIds: result.seriesIds,
    columns: result.columns.map((column) => ({
      time: column.time,
      posTotal: column.posTotal,
      negTotal: column.negTotal,
      posExtent: column.posExtent,
      negExtent: column.negExtent,
      points: column.points.map((p) => ({
        seriesId: p.seriesId,
        value: p.value,
        missing: p.missing,
        lower: p.lower,
        upper: p.upper,
      })),
    })),
  };
}

/**
 * Hiding a series must be indistinguishable from never having included it:
 * stacking with `hidden` ids equals stacking the input with those series
 * removed outright.
 */
export function checkHiddenEquivalence(
  series: readonly SeriesInput[],
  hiddenIds: readonly string[],
  mode: StackMode,
): boolean {
  const hidden = new Set(hiddenIds);
  const withHidden = stackSeries(series, { mode, hidden });
  const removed = stackSeries(
    series.filter((s) => !hidden.has(s.id)),
    { mode },
  );
  return (
    JSON.stringify(normalizeResult(withHidden)) ===
    JSON.stringify(normalizeResult(removed))
  );
}
