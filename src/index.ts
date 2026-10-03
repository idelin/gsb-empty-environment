export { Fraction, dot } from './fraction.ts';
export type { RationalInput } from './fraction.ts';
export { solveLP } from './lp.ts';
export type { LPConstraint, LPResult, LPStatus, Sense } from './lp.ts';
export { formatHyperplane, varName } from './format.ts';
export {
  separate,
  verifySeparation,
  verifyCommonPoint,
} from './separator.ts';
export type {
  InputPoint,
  Separated,
  Intersecting,
  SeparationResult,
} from './separator.ts';
