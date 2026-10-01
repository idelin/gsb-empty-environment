/**
 * Piecewise-linear calibration table over exact decimals.
 *
 * - Forward: x -> y by exact rational interpolation between adjacent points.
 * - Inverse: y -> all x solutions. Non-monotonic tables may yield several
 *   point solutions; a plateau (a segment with y0 == y1) inverts to the
 *   continuous interval [x0, x1]. Touching solutions are merged.
 * - Out-of-range handling is explicit via policies on both directions.
 * - Optional quantization snaps results to a decimal step with an explicit
 *   rounding mode and retains exact error plus worst-case error bounds.
 */

import { Rational } from './rational.ts';
import { parseDecimal, formatRational } from './decimal.ts';
import { quantize, type Quantized, type RoundingMode } from './rounding.ts';

export interface CalibrationPoint {
  x: string;
  y: string;
}

/** Forward policy when the input lies outside the table's x-range. */
export type ForwardRangePolicy = 'error' | 'clamp' | 'extrapolate';

/** Inverse policy when the target has no solution in the table. */
export type InverseRangePolicy = 'error' | 'empty';

export interface QuantizeSpec {
  /** Decimal step, e.g. "0.01". Must be positive. */
  step: string;
  mode: RoundingMode;
}

export class CalibrationConflictError extends Error {
  override readonly name = 'CalibrationConflictError';
}

export class OutOfRangeError extends Error {
  override readonly name = 'OutOfRangeError';
}

export interface QuantizedView {
  value: string;
  exactError: string;
  lowerBound: string;
  upperBound: string;
  raw: Quantized;
}

export interface ForwardResult {
  input: string;
  /** Exact interpolated output. */
  exact: Rational;
  /** Decimal string, or "num/den" when the expansion does not terminate. */
  exactString: string;
  /** Present when the out-of-range policy 'clamp' was applied. */
  clamped: boolean;
  /** Present when the out-of-range policy 'extrapolate' was applied. */
  extrapolated: boolean;
  quantized?: QuantizedView;
}

export type InverseSolution =
  | {
      kind: 'point';
      x: Rational;
      xString: string;
      quantized?: QuantizedView;
    }
  | {
      kind: 'interval';
      from: Rational;
      to: Rational;
      fromString: string;
      toString: string;
    };

export interface InverseResult {
  input: string;
  solutions: InverseSolution[];
}

interface TablePoint {
  x: Rational;
  y: Rational;
}

function quantizedView(q: Quantized): QuantizedView {
  return {
    value: formatRational(q.value),
    exactError: formatRational(q.error),
    lowerBound: formatRational(q.lowerBound),
    upperBound: formatRational(q.upperBound),
    raw: q,
  };
}

export class CalibrationTable {
  /** Deduplicated points with strictly increasing x. */
  readonly points: readonly TablePoint[];

  constructor(points: readonly CalibrationPoint[]) {
    if (points.length < 2) {
      throw new Error('calibration table needs at least two points');
    }
    const parsed: TablePoint[] = points.map((p, i) => {
      try {
        return { x: parseDecimal(p.x), y: parseDecimal(p.y) };
      } catch (err) {
        throw new SyntaxError(
          `invalid decimal at point ${i}: ${(err as Error).message}`,
        );
      }
    });
    const deduped: TablePoint[] = [];
    for (const p of parsed) {
      const last = deduped[deduped.length - 1];
      if (last !== undefined) {
        const order = p.x.cmp(last.x);
        if (order < 0) {
          throw new Error(
            `x values must be non-decreasing; ${formatRational(p.x)} follows ${formatRational(last.x)}`,
          );
        }
        if (order === 0) {
          if (!p.y.eq(last.y)) {
            throw new CalibrationConflictError(
              `conflicting outputs for x=${formatRational(p.x)}: ` +
                `${formatRational(last.y)} vs ${formatRational(p.y)}`,
            );
          }
          continue; // identical duplicate point: drop
        }
      }
      deduped.push(p);
    }
    if (deduped.length < 2) {
      throw new Error('calibration table needs at least two distinct x values');
    }
    this.points = deduped;
  }

  get xMin(): Rational {
    return this.points[0].x;
  }

  get xMax(): Rational {
    return this.points[this.points.length - 1].x;
  }

  private interpolate(index: number, x: Rational): Rational {
    const a = this.points[index];
    const b = this.points[index + 1];
    if (a.y.eq(b.y)) return a.y; // plateau segment
    const t = x.sub(a.x).div(b.x.sub(a.x));
    return a.y.add(t.mul(b.y.sub(a.y)));
  }

  forward(
    input: string,
    options: { policy?: ForwardRangePolicy; quantize?: QuantizeSpec } = {},
  ): ForwardResult {
    const policy = options.policy ?? 'error';
    const x = parseDecimal(input);
    let exact: Rational;
    let clamped = false;
    let extrapolated = false;
    if (x.lt(this.xMin) || x.gt(this.xMax)) {
      if (policy === 'error') {
        throw new OutOfRangeError(
          `input ${input} is outside [${formatRational(this.xMin)}, ${formatRational(this.xMax)}]`,
        );
      }
      if (policy === 'clamp') {
        exact = x.lt(this.xMin) ? this.points[0].y : this.points[this.points.length - 1].y;
        clamped = true;
      } else {
        const index = x.lt(this.xMin) ? 0 : this.points.length - 2;
        exact = this.interpolate(index, x);
        extrapolated = true;
      }
    } else {
      let index = 0;
      while (index < this.points.length - 2 && x.gt(this.points[index + 1].x)) {
        index += 1;
      }
      exact = this.interpolate(index, x);
    }
    const result: ForwardResult = {
      input,
      exact,
      exactString: formatRational(exact),
      clamped,
      extrapolated,
    };
    if (options.quantize !== undefined) {
      const spec = options.quantize;
      result.quantized = quantizedView(
        quantize(exact, parseDecimal(spec.step), spec.mode),
      );
    }
    return result;
  }

  inverse(
    input: string,
    options: { policy?: InverseRangePolicy; quantize?: QuantizeSpec } = {},
  ): InverseResult {
    const policy = options.policy ?? 'error';
    const y = parseDecimal(input);
    const solutions: InverseSolution[] = [];
    const spec = options.quantize;
    const step = spec === undefined ? undefined : parseDecimal(spec.step);

    const pushPoint = (x: Rational): void => {
      const last = solutions[solutions.length - 1];
      if (last !== undefined && last.kind === 'point' && last.x.eq(x)) {
        return; // shared vertex of two segments: count once
      }
      if (last !== undefined && last.kind === 'interval' && last.to.eq(x)) {
        return; // already covered as an interval endpoint
      }
      const solution: InverseSolution = {
        kind: 'point',
        x,
        xString: formatRational(x),
      };
      if (spec !== undefined && step !== undefined) {
        solution.quantized = quantizedView(quantize(x, step, spec.mode));
      }
      solutions.push(solution);
    };

    const pushInterval = (from: Rational, to: Rational): void => {
      const last = solutions[solutions.length - 1];
      if (last !== undefined && last.kind === 'interval' && last.to.eq(from)) {
        // contiguous plateaus merge into one continuous interval
        solutions[solutions.length - 1] = {
          kind: 'interval',
          from: last.from,
          to,
          fromString: last.fromString,
          toString: formatRational(to),
        };
        return;
      }
      if (last !== undefined && last.kind === 'point' && last.x.eq(from)) {
        // a point solution at the plateau edge is absorbed by the interval
        solutions[solutions.length - 1] = {
          kind: 'interval',
          from,
          to,
          fromString: formatRational(from),
          toString: formatRational(to),
        };
        return;
      }
      solutions.push({
        kind: 'interval',
        from,
        to,
        fromString: formatRational(from),
        toString: formatRational(to),
      });
    };

    for (let i = 0; i < this.points.length - 1; i += 1) {
      const a = this.points[i];
      const b = this.points[i + 1];
      if (a.y.eq(b.y)) {
        if (y.eq(a.y)) pushInterval(a.x, b.x); // plateau inverts to an interval
        continue;
      }
      const lo = a.y.lt(b.y) ? a.y : b.y;
      const hi = a.y.lt(b.y) ? b.y : a.y;
      if (y.gte(lo) && y.lte(hi)) {
        const t = y.sub(a.y).div(b.y.sub(a.y));
        pushPoint(a.x.add(t.mul(b.x.sub(a.x))));
      }
    }

    if (solutions.length === 0 && policy === 'error') {
      throw new OutOfRangeError(
        `target ${input} has no solution in the calibration table`,
      );
    }
    return { input, solutions };
  }
}
