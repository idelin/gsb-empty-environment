export * from "./types.ts";
export * from "./pointer.ts";
export * from "./rules.ts";
export {
  analyzeRuleset,
  computeAliasConflicts,
  computeTriggerEdges,
  declaredDomain,
  searchIdempotency,
} from "./dependency.ts";
export {
  applyInversePatch,
  identityOf,
  isStable,
  normalize,
} from "./normalize.ts";
export type { NormalizeOptions } from "./normalize.ts";
export { NormalizationSession, RuleRegistry } from "./registry.ts";
export type {
  IncrementalResult,
  PublishOptions,
  PublishResult,
  PublishedVersion,
  RejectedPublish,
} from "./registry.ts";
