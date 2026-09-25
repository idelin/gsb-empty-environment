export { PathError, ParseError, AccessDeniedError, BudgetExceededError } from './errors.ts';
export { parse, DEFAULT_LIMITS, type ParseLimits } from './parser.ts';
export { compile, CompiledPlan, Execution, DEFAULT_MAX_STEPS, type Match, type RunOptions } from './compiler.ts';
export { evaluate } from './interpreter.ts';
export { StructureCache, formatPath, type CachedResult, type QueryOutcome } from './cache.ts';
export { DENIED_KEYS, getOwn, ownKeys } from './safeAccess.ts';
export type { PathAst, Segment, FilterExpr, Literal } from './ast.ts';
