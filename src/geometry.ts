// Core domain types: segments, boxes, time windows.
//
// Semantics:
//   - Segment time domain is [t0, t1): left-closed, right-open. t0 < t1 is
//     required; a zero-duration segment (t0 === t1) is illegal.
//   - Query boxes are closed on all sides (lo <= p <= hi).
//   - Query time windows are [t0, t1): left-closed, right-open.
//   - Positions and ticks are integers (number or bigint on input, always
//     stored as bigint internally for exact arithmetic).

export type IntInput = number | bigint;

export interface Vec3 {
  readonly x: bigint;
  readonly y: bigint;
  readonly z: bigint;
}

export interface Vec3Input {
  readonly x: IntInput;
  readonly y: IntInput;
  readonly z: IntInput;
}

export interface SegmentInput {
  readonly objectId: string;
  readonly segmentId: string;
  readonly t0: IntInput;
  readonly t1: IntInput;
  readonly p0: Vec3Input;
  readonly p1: Vec3Input;
}

export interface Segment {
  readonly objectId: string;
  readonly segmentId: string;
  readonly t0: bigint;
  readonly t1: bigint;
  readonly p0: Vec3;
  readonly p1: Vec3;
}

export interface Box3Input {
  readonly lo: Vec3Input;
  readonly hi: Vec3Input;
}

export interface Box3 {
  readonly lo: Vec3;
  readonly hi: Vec3;
}

export interface TimeWindowInput {
  readonly t0: IntInput;
  readonly t1: IntInput;
}

export interface TimeWindow {
  readonly t0: bigint;
  readonly t1: bigint;
}

export class InvalidSegmentError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'InvalidSegmentError';
  }
}

export function toBigInt(v: IntInput, field: string): bigint {
  if (typeof v === 'bigint') {
    return v;
  }
  if (typeof v === 'number' && Number.isInteger(v)) {
    return BigInt(v);
  }
  throw new Error('expected integer for ' + field + ', got ' + String(v));
}

export function normalizeVec3(input: Vec3Input, prefix: string): Vec3 {
  return {
    x: toBigInt(input.x, prefix + '.x'),
    y: toBigInt(input.y, prefix + '.y'),
    z: toBigInt(input.z, prefix + '.z'),
  };
}

export function normalizeSegment(input: SegmentInput): Segment {
  const t0 = toBigInt(input.t0, 't0');
  const t1 = toBigInt(input.t1, 't1');
  if (t0 === t1) {
    throw new InvalidSegmentError(
      'zero-duration segment ' + input.segmentId + ' (t0 === t1 === ' + t0.toString() + ')'
    );
  }
  if (t0 > t1) {
    throw new InvalidSegmentError(
      'segment ' + input.segmentId + ' has t0 > t1 (' + t0.toString() + ' > ' + t1.toString() + ')'
    );
  }
  return {
    objectId: input.objectId,
    segmentId: input.segmentId,
    t0: t0,
    t1: t1,
    p0: normalizeVec3(input.p0, 'p0'),
    p1: normalizeVec3(input.p1, 'p1'),
  };
}

export function normalizeBox(input: Box3Input): Box3 {
  const lo = normalizeVec3(input.lo, 'box.lo');
  const hi = normalizeVec3(input.hi, 'box.hi');
  if (lo.x > hi.x || lo.y > hi.y || lo.z > hi.z) {
    throw new Error('invalid query box: lo must be <= hi on every axis');
  }
  return { lo: lo, hi: hi };
}

export function normalizeWindow(input: TimeWindowInput): TimeWindow {
  const t0 = toBigInt(input.t0, 'window.t0');
  const t1 = toBigInt(input.t1, 'window.t1');
  if (t0 >= t1) {
    throw new Error('invalid query window [t0, t1): need t0 < t1, got ' + t0.toString() + ' >= ' + t1.toString());
  }
  return { t0: t0, t1: t1 };
}
