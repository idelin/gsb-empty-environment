/**
 * Piecewise-linear calibration table with exact decimal arithmetic.
 *
 * - Non-monotonic outputs are allowed: a target may have multiple inverse
 *   solutions, and all of them are returned.
 * - Plateau segments (equal consecutive outputs) are allowed: inverting a
 *   plateau value yields a continuous interval.
 * - Duplicate input points with identical outputs are de-duplicated; with
 *   different outputs they raise CalibrationConflictError.
 * - Out-of-range handling is an explicit, required policy per call.
 */

import { Rational } from './rational.ts';
import { quantize, type QuantizedValue, type RoundingMode } from './decimal.ts';

export interface CalibrationPoint {
  x: string;
  y: string;
}

/** Forward policy when the input lies outside the table domain. */
export type ForwardOutOfRangePolicy = 'error' | 'clamp' | 'null';

/** Inverse policy when no input maps to the target. */
export type InverseOutOfRangePolicy = 'error' | 'empty';

export class CalibrationConflictError extends Error {
  readonly x: string;
  readonly outputs: string[];
  constructor(x: string, outputs: string[]) {
    super(`conflicting calibration outputs for input x=${x}: ${outputs.join(' vs ')}`);
    this.name = 'CalibrationConflictError';
    this.x = x;
    this.outputs = outputs;
  }
}

export class OutOfRangeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'OutOfRangeError';
  }
}

export interface ForwardOptions {
  /** Fraction digits of the quantized output. Required and explicit. */
  scale: number;
  /** Rounding rule applied during quantization. Required and explicit. */
  rounding: RoundingMode;
  /** Policy for inputs outside the table domain. Required and explicit. */
  outOfRange: ForwardOutOfRangePolicy;
}

export interface InverseOptions {
  scale: number;
  rounding: RoundingMode;
  /** Policy when the target has no inverse solution. Required and explicit. */
  outOfRange: InverseOutOfRangePolicy;
}

export interface ForwardResult extends QuantizedValue {
  input: string;
  /** True when the input was outside the domain and clamped to an endpoint. */
  clamped: boolean;
}

export interface InverseInterval {
  from: QuantizedValue;
  to: QuantizedValue;
}

export interface InverseResult {
  target: string;
  /** Discrete exact solutions, quantized per the call options. */
  points: QuantizedValue[];
  /** Continuous solution intervals (plateau inversions). */
  intervals: InverseInterval[];
}

interface Node {
  x: Rational;
  y: Rational;
}

export class CalibrationTable {
  private readonly nodes: Node[];

  constructor(points: CalibrationPoint[]) {
    if (points.length < 2) {
      throw new Error('calibration table requires at least two points');
    }
    const parsed = points.map((p) => ({ x: Rational.parse(p.x), y: Rational.parse(p.y) }));
    parsed.sort((a, b) => a.x.compare(b.x));
    const nodes: Node[] = [];
    for (const p of parsed) {
      const last = nodes[nodes.length - 1];
      if (last && last.x.compare(p.x) === 0) {
        if (last.y.compare(p.y) !== 0) {
          throw new CalibrationConflictError(p.x.toDecimalString(), [
            last.y.toDecimalString(),
            p.y.toDecimalString(),
          ]);
        }
        continue; // identical duplicate point: de-duplicated
      }
      nodes.push(p);
    }
    if (nodes.length < 2) {
      throw new Error('calibration table requires at least two distinct input points');
    }
    this.nodes = nodes;
  }

  /** Inclusive input domain of the table. */
  get domain(): { from: string; to: string } {
    return {
      from: this.nodes[0].x.toDecimalString(),
      to: this.nodes[this.nodes.length - 1].x.toDecimalString(),
    };
  }

  /** Forward conversion: input x -> output y (piecewise linear, exact). */
  forward(input: string, options: ForwardOptions): ForwardResult | null {
    const x = Rational.parse(input);
    const first = this.nodes[0];
    const last = this.nodes[this.nodes.length - 1];
    let effective = x;
    let clamped = false;
    if (x.compare(first.x) < 0 || x.compare(last.x) > 0) {
      switch (options.outOfRange) {
        case 'error':
          throw new OutOfRangeError(
            `input x=${input} is outside the calibration domain ` +
              `[${first.x.toDecimalString()}, ${last.x.toDecimalString()}]`,
          );
        case 'null':
          return null;
        case 'clamp':
          effective = x.compare(first.x) < 0 ? first.x : last.x;
          clamped = true;
      }
    }
    let i = 0;
    while (i < this.nodes.length - 2 && this.nodes[i + 1].x.compare(effective) < 0) i++;
    const a = this.nodes[i];
    const b = this.nodes[i + 1];
    // y = a.y + (x - a.x) * (b.y - a.y) / (b.x - a.x), computed exactly.
    const t = effective.sub(a.x).div(b.x.sub(a.x));
    const y = a.y.add(t.mul(b.y.sub(a.y)));
    return { ...quantize(y, options.scale, options.rounding), input, clamped };
  }

  /**
   * Inverse conversion: target y -> every input x mapping to it.
   * Returns all discrete solutions plus any continuous plateau intervals.
   */
  inverse(target: string, options: InverseOptions): InverseResult {
    const ty = Rational.parse(target);
    const pointSolutions: Rational[] = [];
    const intervalSolutions: Array<[Rational, Rational]> = [];
    for (let i = 0; i < this.nodes.length - 1; i++) {
      const a = this.nodes[i];
      const b = this.nodes[i + 1];
      const cmp = a.y.compare(b.y);
      if (cmp === 0) {
        // Plateau segment: the whole x-interval inverts the plateau value.
        if (ty.compare(a.y) === 0) intervalSolutions.push([a.x, b.x]);
        continue;
      }
      const lo = cmp < 0 ? a.y : b.y;
      const hi = cmp < 0 ? b.y : a.y;
      if (ty.compare(lo) < 0 || ty.compare(hi) > 0) continue;
      const t = ty.sub(a.y).div(b.y.sub(a.y));
      pointSolutions.push(a.x.add(t.mul(b.x.sub(a.x))));
    }
    const intervals = mergeIntervals(intervalSolutions);
    const seen = new Set<string>();
    const points: Rational[] = [];
    for (const p of pointSolutions) {
      const key = p.toDecimalString(); // canonical (reduced) representation
      if (seen.has(key)) continue;
      seen.add(key);
      const covered = intervals.some(
        ([lo, hi]) => lo.compare(p) <= 0 && p.compare(hi) <= 0,
      );
      if (!covered) points.push(p);
    }
    if (points.length === 0 && intervals.length === 0 && options.outOfRange === 'error') {
      throw new OutOfRangeError(`no calibration input maps to target y=${target}`);
    }
    return {
      target,
      points: points.map((p) => quantize(p, options.scale, options.rounding)),
      intervals: intervals.map(([lo, hi]) => ({
        from: quantize(lo, options.scale, options.rounding),
        to: quantize(hi, options.scale, options.rounding),
      })),
    };
  }
}

function mergeIntervals(intervals: Array<[Rational, Rational]>): Array<[Rational, Rational]> {
  const sorted = [...intervals].sort((p, q) => p[0].compare(q[0]));
  const merged: Array<[Rational, Rational]> = [];
  for (const [lo, hi] of sorted) {
    const last = merged[merged.length - 1];
    if (last && lo.compare(last[1]) <= 0) {
      if (hi.compare(last[1]) > 0) last[1] = hi;
    } else {
      merged.push([lo, hi]);
    }
  }
  return merged;
}
