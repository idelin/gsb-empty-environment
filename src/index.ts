export type { Evidence, JSONValue, Path, PathSegment, Relation, Row, Tracked } from "./types.ts";
export { PathError, formatPath } from "./errors.ts";
export { LineageStore, evidenceKey, mergeEvidence, type LineageRecord } from "./lineage.ts";
export { trackSource, drill, field, rowEvidence } from "./relation.ts";
export {
  filterRel,
  splitRel,
  joinRel,
  aggregateRel,
  canonical,
  type JoinSpec,
  type MeasureSpec,
  type AggregateSpec,
} from "./ops.ts";
export { Pipeline } from "./pipeline.ts";
