export type {
  CmpOp,
  Database,
  Predicate,
  Query,
  RelInstance,
  RelationSpec,
  Spec,
  Value,
} from "./types.ts";
export { QueryError, evalPredicate, evalQuery } from "./evaluate.ts";
export {
  buildDatabase,
  buildUniverse,
  combinations,
  countDatabases,
  emptyDatabase,
  type UniverseRow,
} from "./enumerate.ts";
export { findSourceRows, type SourceRow } from "./lineage.ts";
export {
  findDifference,
  type DifferenceEntry,
  type DifferentResult,
  type EquivalentResult,
  type SearchOptions,
  type SearchResult,
} from "./search.ts";
