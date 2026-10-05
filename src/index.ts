export {
  createMonotoneRationalSpline,
  DuplicateAbscissaError,
  NonMonotoneDataError,
} from './rationalCubic.ts';
export type {
  MonotoneRationalSpline,
  MonotoneSplineOptions,
  MonotonicityEvidence,
  SegmentCoefficients,
  SplineSegment,
} from './rationalCubic.ts';
export { analyzeSegmentExactly, analyzeSplineExactly } from './exact.ts';
export type { ExactSegmentReport, QuadraticRootAnalysis } from './exact.ts';
export { Fraction } from './fraction.ts';
