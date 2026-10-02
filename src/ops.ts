/**
 * Pipeline operations over exact rationals:
 *  - AffineOp:   y = scale * x + offset           (exact, lossless)
 *  - QuantizeOp: piecewise quantization; each segment owns a step and an
 *                explicit rounding rule. Segments partition the line as
 *                [lo, hi); the last segment's hi is inclusive so a bounded
 *                partition has no hole at its top edge.
 *
 * Every op supports:
 *  - applyOp:      forward evaluation (exact)
 *  - preimageOp:   full input preimage of an output interval, returned as a
 *                  list of intervals that preserve rounding plateaus and
 *                  open/closed endpoints (never merged across plateaus).
 */
import {
  rat, add, mul, sub, div, neg, cmp, toString as ratToString,
} from "./rational.ts";
import type {
  Rat,
} from "./rational.ts";
import {
  interval, intersect, sortIntervals,
} from "./interval.ts";
import type {
  Interval,
} from "./interval.ts";
import {
  quantize, maxQuantizeLoss, plateauForLevel,
} from "./rounding.ts";
import type {
  RoundingRule,
} from "./rounding.ts";

export interface AffineOp {
  kind: "affine";
  scale: Rat; // != 0
  offset: Rat;
  name?: string;
}

export interface QuantizeSegment {
  lo: Rat | null; // inclusive; null = -inf
  hi: Rat | null; // exclusive, except on the last segment where it is inclusive
  step: Rat; // > 0
  rule: RoundingRule;
}

export interface QuantizeOp {
  kind: "quantize";
  segments: QuantizeSegment[];
  name?: string;
}

export type Op = AffineOp | QuantizeOp;

export function affine(scale: Rat, offset: Rat, name?: string): AffineOp {
  if (scale.n === 0n) throw new Error("affine scale must be non-zero");
  return { kind: "affine", scale, offset, name };
}

export function quantizeOp(segments: QuantizeSegment[], name?: string): QuantizeOp {
  if (segments.length === 0) throw new Error("quantize op needs >= 1 segment");
  for (const s of segments) {
    if (s.step.n <= 0n) throw new Error("segment step must be positive");
    if (s.lo !== null && s.hi !== null && cmp(s.lo, s.hi) > 0)
      throw new Error("segment lo > hi");
  }
  return { kind: "quantize", segments, name };
}

export function opName(op: Op, index: number): string {
  return op.name ?? `op#${index}`;
}

export function describeOp(op: Op): string {
  if (op.kind === "affine")
    return `affine(scale=${ratToString(op.scale)}, offset=${ratToString(op.offset)})`;
  const segs = op.segments
    .map((s) => `[${s.lo === null ? "-inf" : ratToString(s.lo)}, ${s.hi === null ? "+inf" : ratToString(s.hi)}) step=${ratToString(s.step)} ${s.rule}`)
    .join(" | ");
  return `quantize(${segs})`;
}

/** Locate the segment containing x (last segment's hi is inclusive). */
export function findSegment(op: QuantizeOp, x: Rat): QuantizeSegment {
  for (let i = 0; i < op.segments.length; i++) {
    const s = op.segments[i];
    if (s.lo !== null && cmp(x, s.lo) < 0) continue;
    if (s.hi !== null) {
      const c = cmp(x, s.hi);
      if (c > 0) continue;
      if (c === 0 && i !== op.segments.length - 1) continue;
    }
    return s;
  }
  throw new Error(`no quantize segment covers x = ${ratToString(x)}`);
}

export function applyOp(op: Op, x: Rat): Rat {
  if (op.kind === "affine") return add(mul(op.scale, x), op.offset);
  const seg = findSegment(op, x);
  return quantize(x, seg.step, seg.rule);
}

/** Worst-case loss this op can add to a value passing through it. */
export function opMaxLoss(op: QuantizeOp): Rat {
  let m = rat(0n);
  for (const s of op.segments) {
    const l = maxQuantizeLoss(s.step, s.rule);
    if (cmp(l, m) > 0) m = l;
  }
  return m;
}

function affinePreimage(op: AffineOp, out: Interval): Interval {
  const map = (e: { v: Rat; closed: boolean } | null) =>
    e === null ? null : { v: div(sub(e.v, op.offset), op.scale), closed: e.closed };
  const lo = map(out.lo);
  const hi = map(out.hi);
  // Negative scale flips the order; endpoint openness travels with the value.
  return op.scale.n > 0n ? { lo, hi } : { lo: hi, hi: lo };
}

function segmentDomain(seg: QuantizeSegment, isLast: boolean): Interval {
  return interval(seg.lo, true, seg.hi, isLast);
}

/**
 * Preimage of `out` under one quantize segment: one interval per output
 * level (rounding plateau), intersected with the segment domain.
 */
function quantizeSegmentPreimage(seg: QuantizeSegment, isLast: boolean, out: Interval): Interval[] {
  const domain = segmentDomain(seg, isLast);
  const step = seg.step;
  // Generous integer bounds on the output level k (filtered exactly below).
  // k*step must lie in `out` and its plateau must touch the domain.
  let kLo: bigint | null = null;
  let kHi: bigint | null = null;
  const lowerSources: Rat[] = [];
  const upperSources: Rat[] = [];
  if (out.lo !== null) lowerSources.push(out.lo.v);
  if (seg.lo !== null) lowerSources.push(sub(seg.lo, step));
  if (out.hi !== null) upperSources.push(out.hi.v);
  if (seg.hi !== null) upperSources.push(add(seg.hi, step));
  if (lowerSources.length > 0) {
    const lo = lowerSources.reduce((a, b) => (cmp(a, b) >= 0 ? a : b));
    kLo = floorDivRat(lo, step) - 1n;
  }
  if (upperSources.length > 0) {
    const hi = upperSources.reduce((a, b) => (cmp(a, b) <= 0 ? a : b));
    kHi = ceilDivRat(hi, step) + 1n;
  }
  if (kLo === null || kHi === null)
    throw new Error(
      "quantize preimage is unbounded; bound the output interval or the segment domain",
    );

  const result: Interval[] = [];
  for (let k = kLo; k <= kHi; k++) {
    const level = mul(rat(k), step);
    if (!containsEndpointAware(out, level)) continue;
    const p = plateauForLevel(k, step, seg.rule);
    const plateauIv = interval(p.lo, p.loClosed, p.hi, p.hiClosed);
    const clipped = intersect(plateauIv, domain);
    if (clipped !== null) result.push(clipped);
  }
  return result;
}

function containsEndpointAware(iv: Interval, x: Rat): boolean {
  if (iv.lo !== null) {
    const c = cmp(x, iv.lo.v);
    if (c < 0 || (c === 0 && !iv.lo.closed)) return false;
  }
  if (iv.hi !== null) {
    const c = cmp(x, iv.hi.v);
    if (c > 0 || (c === 0 && !iv.hi.closed)) return false;
  }
  return true;
}

function floorDivRat(a: Rat, b: Rat): bigint {
  const q = div(a, b);
  const f = q.n / q.d;
  return q.n % q.d !== 0n && q.n < 0n ? f - 1n : f;
}
function ceilDivRat(a: Rat, b: Rat): bigint {
  return -floorDivRat(neg(a), b);
}

/**
 * Full input preimage of an output interval. Affine: one exact interval.
 * Quantize: one interval per rounding plateau per segment, sorted,
 * plateaus kept separate so tie-point ownership stays visible.
 */
export function preimageOp(op: Op, out: Interval): Interval[] {
  if (op.kind === "affine") return [affinePreimage(op, out)];
  const result: Interval[] = [];
  for (let i = 0; i < op.segments.length; i++)
    result.push(...quantizeSegmentPreimage(op.segments[i], i === op.segments.length - 1, out));
  return sortIntervals(result);
}

/** Preimage of `out` through the whole pipeline (ops applied in reverse). */
export function pipelinePreimage(ops: Op[], out: Interval): Interval[] {
  let ivs: Interval[] = [out];
  for (let i = ops.length - 1; i >= 0; i--) {
    const next: Interval[] = [];
    for (const iv of ivs) next.push(...preimageOp(ops[i], iv));
    ivs = sortIntervals(next);
  }
  return ivs;
}
