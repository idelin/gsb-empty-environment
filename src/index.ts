export type {
  Expr,
  JsonPathAst,
  JsonValue,
  PathMode,
  PathVars,
  Step,
  TriState,
} from './ast.ts';
export { JsonPathError, isStructuralError } from './errors.ts';
export type { JsonPathErrorCode } from './errors.ts';
export { parseJsonPath, Parser } from './parser.ts';
export {
  deepEqual,
  JsonPathEvaluator,
  jsonTypeName,
  matchJsonPath,
  queryJsonPath,
} from './evaluator.ts';
export type { EvalOptions } from './evaluator.ts';
export {
  canonicalScalar,
  extractIndexPlan,
  FULL_SCAN,
  intersectPlans,
  unionPlans,
} from './index-plan.ts';
export type { Conjunction, IndexFact, IndexPlan } from './index-plan.ts';
export { collectFacts, encodeFact, GinPathIndex } from './gin-index.ts';
