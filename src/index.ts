export type { JsonValue, JsonObject, Path } from "./json.ts";
export { pathToPointer, pointerToPath, deepEqual, deepClone } from "./json.ts";
export { DELETE, isDeleteSentinel } from "./sentinel.ts";
export {
  ConfigError,
  MergeTypeConflictError,
  FieldValidationError,
  RefResolutionError,
  RefCycleError,
} from "./errors.ts";
export { OVERRIDE, DEEP, byId, StrategyMap } from "./strategy.ts";
export type { MergeStrategy, StrategyRule } from "./strategy.ts";
export { isRefNode } from "./refs.ts";
export type { Contribution, ExplainRecord } from "./merge.ts";
export { compileFromScratch, hotUpdateLayer, diffTrees } from "./compiler.ts";
export type { ActiveLayer, CompiledState } from "./compiler.ts";
export { LayeredConfigStore } from "./store.ts";
export type { LayerDef, Snapshot, Subscriber, StoreOptions } from "./store.ts";
