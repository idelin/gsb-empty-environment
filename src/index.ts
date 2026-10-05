export { MonotoneRationalSpline, createSpline } from './spline.ts';
export type {
  SegmentCoefficients,
  SegmentTrend,
  MonotonicityCertificate,
  VerificationReport,
  SplineOptions,
} from './spline.ts';
export { pchipDerivatives } from './derivatives.ts';
export { analyzeQuadraticSign } from './quadratic.ts';
export type { QuadraticSignAnalysis, QuadraticClassification } from './quadratic.ts';
export { DuplicateAbscissaError, InvalidInputError, NotMonotoneError } from './errors.ts';
