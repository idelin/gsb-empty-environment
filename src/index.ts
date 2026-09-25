export { PathError, type ErrorCode } from './errors.ts';
export { parsePath, LIMITS, FORBIDDEN_KEYS } from './parser.ts';
export { compilePath, compileAst, filterCost, type Plan, type SegmentOp } from './plan.ts';
export { PlanCursor, executePlan, type Match, type PathSeg } from './vm.ts';
export { evaluateSlow } from './interpreter.ts';
export { DocumentStore, queryOnce, DEFAULT_BUDGET, type QueryResult } from './store.ts';
export {
  isPlainObject,
  isTraversable,
  assertDataValue,
  assertDataTree,
  readOwn,
  readIndex,
  ownEntries,
} from './safeobj.ts';
export type { PathAst } from './ast.ts';
