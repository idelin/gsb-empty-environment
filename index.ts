export {
  canUseSharedArrayBuffer,
  checkCrossOriginIsolation,
  evaluateCrossOriginIsolation,
  isCrossOriginIsolated,
  type CrossOriginIsolationResult,
  type HeaderPair,
  type HeaderSource,
  type IsolationFailure,
  type IsolationHeader,
  type PolicyEvaluation,
  type PolicyStatus,
} from "./src/isolation.ts";

export {
  parseStructuredFieldItem,
  type StructuredFieldBareItem,
  type StructuredFieldItem,
} from "./src/structured-fields.ts";
