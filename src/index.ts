export type {
  Condition,
  ConditionTrace,
  EngineResult,
  FactValue,
  Facts,
  FieldConflict,
  FieldEvidence,
  FieldStatus,
  Rule,
  RuleEvidence,
  RuleOutcome,
} from './types.ts';
export { evaluate } from './condition.ts';
export { runEngine } from './engine.ts';
export { runReference } from './reference.ts';
export { canonicalize, normalizeResult } from './canonical.ts';
export { checkOrderIndependence } from './orderCheck.ts';
export type { OrderCheckOptions, OrderCheckReport, OrderMismatch } from './orderCheck.ts';
