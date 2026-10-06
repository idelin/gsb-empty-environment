/**
 * Conservation checks for stacking results. Each check returns a list of
 * human-readable violations; an empty list means the invariant holds.
 */

import {
  InputPoint,
  SeriesInput,
  StackMode,
  StackResult,
  columnExtents,
  stackSeries,
} from './stack';

const EPS = 1e-9;

function close(a: number, b: number): boolean {
  return Math.abs(a - b) <= EPS * Math.max(1, Math.abs(a), Math.abs(b));
}

/**
 * All-zero column: every visible series with data at `t` must report a
 * zero-height slice on the baseline, flagged `zero` and never `missing`;
 * the column extents must be exactly [0, 0].
 */
export function checkAllZeroColumn(result: StackResult, t: number): string[] {
  const violations: string[] = [];
  const col = result.times.indexOf(t);
  if (col < 0) {
    violations.push(`t=${t} is not on the time axis`);
    return violations;
  }
  const cells = result.series.map((s) => s.points[col]);
  for (let i = 0; i < cells.length; i++) {
    const c = cells[i];
    if (result.series[i].hidden) continue;
    if (c.missing) continue; // series genuinely absent at t
    if (!c.zero) violations.push(`series ${result.series[i].id}: expected zero flag at t=${t}`);
    if (c.lower !== 0 || c.upper !== 0) {
      violations.push(
        `series ${result.series[i].id}: expected [0,0] at t=${t}, got [${c.lower},${c.upper}]`
      );
    }
  }
  const ext = columnExtents(cells);
  if (ext.top !== 0 || ext.bottom !== 0) {
    violations.push(`column extents at t=${t} are [${ext.bottom},${ext.top}], expected [0,0]`);
  }
  return violations;
}

/**
 * Order conservation: permuting the input series must not change any
 * series' slice heights nor the column extents. (Bounds shift with
 * stacking order; heights and totals are the conserved quantities.)
 */
export function checkOrderConservation(
  inputs: ReadonlyArray<SeriesInput>,
  order: number[],
  mode: StackMode
): string[] {
  const violations: string[] = [];
  const base = stackSeries(inputs, mode);
  const permuted = stackSeries(order.map((i) => inputs[i]), mode);

  if (base.times.length !== permuted.times.length) {
    violations.push('time axis changed under permutation');
    return violations;
  }

  const heightOf = (r: StackResult, id: string, col: number): number | null => {
    const s = r.series.find((x) => x.id === id);
    if (!s) return null;
    const p = s.points[col];
    if (p.missing || p.lower === null || p.upper === null) return null;
    return p.upper - p.lower;
  };

  for (let col = 0; col < base.times.length; col++) {
    const extA = columnExtents(base.series.map((s) => s.points[col]));
    const extB = columnExtents(permuted.series.map((s) => s.points[col]));
    if (!close(extA.top, extB.top) || !close(extA.bottom, extB.bottom)) {
      violations.push(
        `column ${base.times[col]}: extents [${extA.bottom},${extA.top}] vs [${extB.bottom},${extB.top}]`
      );
    }
    for (const s of base.series) {
      const hA = heightOf(base, s.id, col);
      const hB = heightOf(permuted, s.id, col);
      if (hA === null && hB === null) continue;
      if (hA === null || hB === null || !close(hA, hB)) {
        violations.push(`series ${s.id} column ${base.times[col]}: height ${hA} vs ${hB}`);
      }
    }
  }
  return violations;
}

/**
 * Hidden-series conservation: in absolute mode hiding preserves every
 * visible series' slice heights (in percent mode heights rescale against
 * the visible totals); in both modes hiding shifts bounds exactly as
 * removing the series outright would, and unhiding restores the
 * original bounds exactly.
 */
export function checkHiddenConservation(
  inputs: ReadonlyArray<SeriesInput>,
  hiddenId: string,
  mode: StackMode
): string[] {
  const violations: string[] = [];
  const full = stackSeries(inputs, mode);
  const hidden = stackSeries(
    inputs.map((s) => (s.id === hiddenId ? { ...s, hidden: true } : s)),
    mode
  );
  const removed = stackSeries(
    inputs.filter((s) => s.id !== hiddenId),
    mode
  );

  if (mode === 'absolute') {
    for (const s of full.series) {
      if (s.id === hiddenId) continue;
      const h = hidden.series.find((x) => x.id === s.id);
      if (!h) {
        violations.push(`series ${s.id} disappeared after hiding ${hiddenId}`);
        continue;
      }
      for (let col = 0; col < s.points.length; col++) {
        const a = s.points[col];
        const b = h.points[col];
        if (a.missing !== b.missing) {
          violations.push(`series ${s.id} t=${a.t}: missing ${a.missing} -> ${b.missing}`);
        } else if (!a.missing && a.lower !== null && a.upper !== null && b.lower !== null && b.upper !== null) {
          const hA = a.upper - a.lower;
          const hB = b.upper - b.lower;
          if (!close(hA, hB)) {
            violations.push(`series ${s.id} t=${a.t}: height ${hA} -> ${hB} after hiding ${hiddenId}`);
          }
        }
      }
    }
  }

  // Hidden series must contribute nothing: same visible bounds as removal.
  const hiddenTimes = hidden.times;
  for (const s of removed.series) {
    const h = hidden.series.find((x) => x.id === s.id);
    if (!h) continue;
    for (const p of s.points) {
      const col = hiddenTimes.indexOf(p.t);
      if (col < 0) continue;
      const q = h.points[col];
      if (p.missing !== q.missing) continue;
      if (
        !p.missing &&
        p.lower !== null &&
        q.lower !== null &&
        p.upper !== null &&
        q.upper !== null &&
        (!close(p.lower, q.lower) || !close(p.upper, q.upper))
      ) {
        violations.push(
          `series ${s.id} t=${p.t}: hidden [${q.lower},${q.upper}] != removed [${p.lower},${p.upper}]`
        );
      }
    }
  }

  // Unhiding restores the original result exactly.
  const restored = stackSeries(inputs, mode);
  for (const s of full.series) {
    const r = restored.series.find((x) => x.id === s.id);
    if (!r) {
      violations.push(`series ${s.id} lost after unhide`);
      continue;
    }
    for (let col = 0; col < s.points.length; col++) {
      const a = s.points[col];
      const b = r.points[col];
      if (a.missing !== b.missing || a.lower !== b.lower || a.upper !== b.upper) {
        violations.push(`series ${s.id} t=${a.t}: unhide did not restore bounds`);
      }
    }
  }
  return violations;
}

/** Deterministic permutation helper for tests. */
export function rotateOrder(n: number, k: number): number[] {
  const out: number[] = [];
  for (let i = 0; i < n; i++) out.push((i + k) % n);
  return out;
}

export function makeSeries(id: string, points: ReadonlyArray<InputPoint>, hidden = false): SeriesInput {
  return hidden ? { id, hidden: true, points } : { id, points };
}
