export type {
  AtomicWriteOperation,
  BaseOperation,
  CopyOperation,
  CreateOperation,
  InferenceInput,
  InferenceResult,
  MoveOperation,
  Operation,
  OperationType,
  PathResult,
  PathRule,
  ProtectionClass,
  RestoreOperation,
  SetAttributesOperation,
  Violation,
} from './types.ts';
export { InferenceError } from './types.ts';

export {
  isStronger,
  isWeaker,
  meetsRequirement,
  PROTECTION_CLASSES_ORDERED,
  strengthOf,
  strongerOf,
} from './classes.ts';

export { matchesPrefix, normalizePath, strongestRequirement } from './rules.ts';

export { inferProtectionClasses } from './engine.ts';
