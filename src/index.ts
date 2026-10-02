export { Rat, type RatInput } from "./rational.ts";
export {
  analyze,
  createCurve,
  derivativePolynomial,
  numeratorDenominator,
  type AnalysisResult,
  type AnalyzeOptions,
  type CriticalPoint,
  type Extremum,
  type RationalBezier1D,
  type Segment,
  type TRef,
} from "./rationalBezier.ts";
export {
  isolateRoots,
  refineRoot,
  sturmSequence,
  polyToString,
  type IsolatedRoot,
  type IntPoly,
  type RatPoly,
} from "./poly.ts";
