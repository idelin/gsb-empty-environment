export { migrate, unmigrate } from './migrate.js';
export { rename, split, merge } from './rules.js';
export { MigrationConflictError } from './errors.js';
export { deepEqual, deepClone } from './equal.js';
export type {
  ConfigObject,
  ForwardResult,
  LossEntry,
  MigrateResult,
  MigrationRule,
  ProvenanceEntry,
  RuleLoss,
} from './types.js';
export type { RuleOptions } from './rules.js';
