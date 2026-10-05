export { Graph, type Edge, type EdgeId, type Label, type NodeId } from "./graph.ts";
export {
  Automaton,
  type AutomatonSpec,
  type AutomatonState,
  type AutomatonTransition,
} from "./automaton.ts";
export {
  compareEdgeIds,
  compareEvidence,
  productStateKey,
  regularPathQuery,
  type PathEvidence,
  type ProductState,
  type QueryOptions,
} from "./query.ts";
export {
  independentProductBfs,
  replayEvidence,
  verifyQueryResults,
  type ProductBfsResult,
  type ReachableAcceptingState,
  type VerificationReport,
} from "./verify.ts";
