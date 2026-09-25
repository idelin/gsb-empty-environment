export {
  deepEqual,
  formatPath,
  isPlainObject,
  type JsonObject,
  type JsonValue,
  type Path,
} from "./json.ts";
export { MergeError, type MergeErrorCode, type MergeSide } from "./errors.ts";
export {
  merge3,
  pickResolution,
  type Conflict,
  type ConflictReason,
  type MergeOptions,
  type MergeResult,
  type Resolution,
} from "./merge.ts";
export { resolveAll, resolveConflict, type ResolveOptions } from "./resolve.ts";
