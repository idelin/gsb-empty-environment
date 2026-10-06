/**
 * Core stacking engine.
 *
 * Semantics:
 * - Series are aligned on the sorted union of all timestamps.
 * - A series with no point at a timestamp is `missing` (null bounds); an
 *   explicit value of 0 is `zero` (zero-height bounds), never missing.
 * - Positive values accumulate upward from 0, negative values downward
 *   from 0; the two sides are independent.
 * - In `percent` mode, positive heights are normalized by the column's
 *   total positive sum and negative heights by the column's total
 *   absolute negative sum, so each side spans [0, 1] / [0, -1].
 * - Hidden series are aligned on the time axis but excluded from
 *   accumulation; all of their points report null bounds.
 */

export type StackMode = 'absolute' | 'percent';

export interface InputPoint {
  t: number;
  v: number;
}

export interface SeriesInput {
  id: string;
  hidden?: boolean;
  points: ReadonlyArray<InputPoint>;
}

export interface StackCell {
  lower: number | null;
  upper: number | null;
  missing: boolean;
  zero: boolean;
  value: number | null;
}

export interface StackPoint extends StackCell {
  t: number;
}

export interface StackedSeries {
  id: string;
  hidden: boolean;
  points: StackPoint[];
}

export interface StackResult {
  mode: StackMode;
  times: number[];
  series: StackedSeries[];
}

const EPS = 1e-12;

/**
 * Stacks one time column. `values[i]` is the value of series i at this
 * timestamp, or null when the series has no data there. Hidden series
 * must be excluded by the caller (pass null / omit them).
 *
 * Returns one cell per input value, in input order.
 */
export function stackColumn(values: ReadonlyArray<number | null>, mode: StackMode): StackCell[] {
  let posSum = 0;
  let negSum = 0;
  for (const v of values) {
    if (v === null) continue;
    if (v > 0) posSum += v;
    else if (v < 0) negSum += -v;
  }

  const scalePos = mode === 'percent' && posSum > EPS ? 1 / posSum : 1;
  const scaleNeg = mode === 'percent' && negSum > EPS ? 1 / negSum : 1;

  const cells: StackCell[] = [];
  let posBase = 0;
  let negBase = 0;
  for (const v of values) {
    if (v === null) {
      cells.push({ lower: null, upper: null, missing: true, zero: false, value: null });
      continue;
    }
    if (v > 0) {
      const h = v * scalePos;
      cells.push({ lower: posBase, upper: posBase + h, missing: false, zero: false, value: v });
      posBase += h;
    } else if (v < 0) {
      const h = v * scaleNeg;
      cells.push({ lower: negBase + h, upper: negBase, missing: false, zero: false, value: v });
      negBase += h;
    } else {
      // Explicit zero: zero-height slice parked on the positive baseline.
      cells.push({ lower: posBase, upper: posBase, missing: false, zero: true, value: 0 });
    }
  }
  return cells;
}

/**
 * Aligns series on the union of their timestamps and stacks every column.
 * Stacking order follows the order of `inputs`; hidden series keep their
 * slot (and timestamps) but contribute nothing.
 */
export function stackSeries(inputs: ReadonlyArray<SeriesInput>, mode: StackMode): StackResult {
  const timeSet = new Set<number>();
  const lookup: Array<Map<number, number>> = inputs.map((s) => {
    const m = new Map<number, number>();
    for (const p of s.points) {
      if (m.has(p.t)) {
        throw new Error(`series "${s.id}" has duplicate point at t=${p.t}`);
      }
      m.set(p.t, p.v);
      timeSet.add(p.t);
    }
    return m;
  });
  const times = Array.from(timeSet).sort((a, b) => a - b);

  const series: StackedSeries[] = inputs.map((s) => ({
    id: s.id,
    hidden: s.hidden === true,
    points: [],
  }));

  for (const t of times) {
    const values = inputs.map((s, i) =>
      s.hidden === true ? null : lookup[i].has(t) ? (lookup[i].get(t) as number) : null
    );
    const cells = stackColumn(values, mode);
    for (let i = 0; i < inputs.length; i++) {
      const cell = inputs[i].hidden === true
        ? { lower: null, upper: null, missing: true, zero: false, value: null }
        : cells[i];
      series[i].points.push({ t, ...cell });
    }
  }

  return { mode, times, series };
}

/** Sums the visible (non-missing) values of one column. */
export function columnSums(cells: ReadonlyArray<StackCell>): { pos: number; neg: number } {
  let pos = 0;
  let neg = 0;
  for (const c of cells) {
    if (c.missing || c.value === null) continue;
    if (c.value > 0) pos += c.value;
    else if (c.value < 0) neg += c.value;
  }
  return { pos, neg };
}

/** Top of the positive stack and bottom of the negative stack for a column. */
export function columnExtents(cells: ReadonlyArray<StackCell>): { top: number; bottom: number } {
  let top = 0;
  let bottom = 0;
  for (const c of cells) {
    if (c.missing || c.upper === null || c.lower === null) continue;
    if (c.upper > top) top = c.upper;
    if (c.lower < bottom) bottom = c.lower;
  }
  return { top, bottom };
}
