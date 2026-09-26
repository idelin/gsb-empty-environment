export type { Condition, Group, GroupKind, Json, Schema, UndecidedPolicy } from './types.ts';
export { hasField, deepEqual } from './presence.ts';
export {
  evalCondition,
  conditionFields,
  equalsLiterals,
  missingEqualsFields,
  type Tri,
  type FieldGetter,
  type FieldState,
} from './condition.ts';
export {
  validate,
  assertSchema,
  instanceGetter,
  SchemaError,
  type ValidationError,
  type ValidationResult,
} from './validate.ts';
export {
  Solver,
  ABSENT,
  OTHER,
  jointSatisfiable,
  findWitness,
  type Assignment,
  type CellValue,
  type SolveResult,
  type Witness,
} from './solver.ts';
export {
  minimalUnsatCore,
  minimalCorrections,
  materialize,
  type UnsatCore,
  type Correction,
} from './core.ts';
