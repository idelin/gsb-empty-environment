export { Rational, r, type RationalInput } from "./rational.ts";
export {
  projectControlPoints,
  type ConflictResult,
  type ConstraintInfo,
  type ControlPointInput,
  type OptimalResult,
  type ProjectionInput,
  type ProjectionResult,
  type SegmentInput,
  type VariableRef,
  type Vec2,
  type Vec2Input,
  type Verification,
} from "./projection.ts";
export {
  solveLinear,
  nullspaceOf,
  rref,
  matVecMul,
  matTVecMul,
  type Matrix,
  type Vector,
  type LinearSolution,
} from "./linalg.ts";
