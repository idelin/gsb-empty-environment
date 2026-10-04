export type { JsonValue, CmpOp, Expr, PathSegment, Query } from './ast.ts';
export { deepEqual, isObject } from './ast.ts';
export type { Path, PathSeg } from './path.ts';
export { canonical, parseCanonical, isPrefix } from './path.ts';
export {
  QueryError,
  ParseError,
  BudgetExceededError,
  BufferOverflowError,
  CancelledError,
  EditError,
} from './errors.ts';
export type { BudgetPhase } from './errors.ts';
export type { QueryBudget } from './budget.ts';
export { BudgetTracker, UNLIMITED_BUDGET } from './budget.ts';
export { parse } from './parser.ts';
export type { Program, FilterPlan, CompiledSegment } from './compiler.ts';
export { compile, compileExpr, needsContainerEnd } from './compiler.ts';
export type { Match, EvalOptions } from './interpreter.ts';
export { evaluate, evaluateFrom, sliceIndices } from './interpreter.ts';
export type { Event, NodeKind } from './events.ts';
export { walk, kindOf } from './events.ts';
export type { StreamOptions, StreamResult } from './stream.ts';
export { runStream, streamQuery } from './stream.ts';
export type { Edit, Cursor, ResultDiff } from './incremental.ts';
export {
  VersionedDocument,
  IncrementalResults,
  cursorOf,
  resolveCursor,
  setPath,
  deletePath,
} from './incremental.ts';
