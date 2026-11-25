/**
 * Merge acceptance package for a TypeScript configuration host.
 *
 * Import `acceptMerge` (pure) or `publishAccepted` (host-sink, zero publish on
 * failure). The DSL helpers make small truth trees concise.
 */

export { acceptMerge, publishAccepted } from './accept.ts';
export { Authority } from './authority.ts';
export type { FacetRequest } from './authority.ts';
export { MergeEngine } from './engine.ts';
export {
  canonicalJson,
  errorSortKey,
  joinPointer,
  makeError,
  parentPointer,
  patternMatches,
  pointerSegments,
  shortHash,
  sortErrors,
} from './locations.ts';
export {
  doc,
  docFromPaths,
  branch,
  del,
  obj,
  leaf,
  nil,
  tomb,
  ref,
  resetDslSequence,
} from './dsl.ts';
export type { Marker, NodeOpts, DocOptions } from './dsl.ts';
export { findByPointer, indexDoc, originalSubtreeIds } from './tree-index.ts';

export { truthOracle, exhaustiveStateTruth } from './truth/oracle.ts';
export { CANONICAL_SCENARIOS } from './truth/scenarios.ts';

export type {
  AcceptanceError,
  AcceptanceResult,
  AcceptOptions,
  AliasRule,
  Attribution,
  Branch,
  BranchId,
  BranchTraceEntry,
  Condition,
  DeleteDirective,
  DocId,
  ErrorCode,
  ErrorLocation,
  Evidence,
  ExposureRecord,
  Facet,
  FacetDecision,
  Grant,
  LeafProvenance,
  LeafValue,
  MergeInput,
  MergeTrace,
  Node,
  NodeId,
  NodeKind,
  ObjectNode,
  ValueNode,
  NullNode,
  TombstoneNode,
  RefNode,
  PublishedLeaf,
  Publication,
  PublishHost,
  ProtectedKeyRule,
  Rejection,
  Revocation,
  Signature,
  SigId,
  TemplateId,
  TreeDoc,
} from './types.ts';
export { FACETS } from './types.ts';

// Convenience aliases for hosts/tests that prefer domain verbs.
export { acceptMerge as acceptConfigMerge } from './accept.ts';
export { acceptMerge as verifyMerge } from './accept.ts';
export { acceptMerge as evaluateMerge } from './accept.ts';
export { publishAccepted as publishMerge } from './accept.ts';
