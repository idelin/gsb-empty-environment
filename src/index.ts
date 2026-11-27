export type {
  Assignment,
  AssignmentResolver,
  AssignmentValue,
  AssignmentValueResolver,
  BranchCondition,
  DataRow,
  DeleteAction,
  DoNothingAction,
  InsertAction,
  MatchPredicate,
  MergeAction,
  MergeActionObject,
  MergeActionKind,
  MergeBranch,
  MergeContext,
  MergePhase,
  MergeResult,
  MergeStatement,
  MergeStats,
  MergeTableInput,
  NullsMode,
  UniqueIndex,
  UniqueIndexInput,
  UniqueTiming,
  UniqueViolation,
  UpdateAction,
} from "./types.js";

export {
  CARDINALITY_VIOLATION,
  CardinalityViolationError,
  INVALID_MERGE_STATEMENT,
  MergeError,
  UNIQUE_VIOLATION,
  UniqueViolationError,
} from "./errors.js";
export type {
  CardinalityViolationDetails,
  MergeErrorCode,
  UniqueViolationDetails,
} from "./errors.js";

export {
  checkUniqueConstraints,
  findUniqueViolation,
  normalizeUniqueIndexes,
  validateNormalizedUniqueConstraints,
  validateUniqueConstraints,
} from "./constraints.js";
export type { NormalizedUniqueIndex } from "./constraints.js";

export {
  MergeTable,
  applyMerge,
  deleteRow,
  doNothing,
  executeMerge,
  executeSqlMerge,
  insert,
  merge,
  mergeInto,
  mergeRows,
  noop,
  runMerge,
  update,
} from "./merge.js";
export type {
  MergeOperation,
  MergeTableOptions,
} from "./merge.js";

import { executeMerge } from "./merge.js";

export default executeMerge;
