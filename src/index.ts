export type { JsonValue, JsonObject, Query, Segment, SimpleSegment, Expr, QueryResult } from "./ast.ts";
export { needsStaging } from "./ast.ts";
export { Budget, type BudgetLimits } from "./budget.ts";
export {
  QuerySyntaxError,
  BudgetExceededError,
  DocumentEditError,
  CancelledError,
  type BudgetKind,
} from "./errors.ts";
export { parseQuery } from "./parser.ts";
export {
  evaluate,
  evaluateSegments,
  evalExpr,
  exprPasses,
  jsonEqual,
  sliceIndices,
  matchSegmentChildren,
} from "./interpreter.ts";
export { walkEvents, buildValue, type TreeEvent } from "./events.ts";
export { streamQuery, collectStream, type StreamOptions } from "./stream.ts";
export { FileSpillStore, type SpillWriter } from "./spill.ts";
export { VersionedDocument, applyEdit, type Edit } from "./document.ts";
export {
  IncrementalQuery,
  type ResultDiff,
  type Cursor,
  type CursorResolution,
} from "./incremental.ts";
export {
  encodePath,
  formatPath,
  escapeSeg,
  unescapeSeg,
  isPrefix,
  pathEquals,
  type Path,
  type PathSeg,
} from "./path.ts";
