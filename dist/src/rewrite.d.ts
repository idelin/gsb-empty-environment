import type { ColumnRef, ContextLike, Plan, QueryResult, Row } from "./types.js";
import { QueryExecutionError } from "./eval.js";
export declare class RewriteError extends QueryExecutionError {
    constructor(message: string);
}
export declare function freeColumns(plan: Plan, contextLike?: ContextLike): ColumnRef[];
export declare class GeneralUnnestingRewriter {
    private readonly context;
    private counter;
    constructor(contextLike?: ContextLike);
    private fresh;
    rewrite(plan: Plan): Plan;
    private rewritePlan;
    private rewritePlanNode;
    private rewriteExpr;
    private applySubquery;
    private buildDomain;
    private decorrelatePlan;
    private decorrelateAggregate;
    private decorrelateLimit;
    private renameKeys;
    private foldNamedKeys;
}
export declare function rewriteQuery(plan: Plan, contextLike?: ContextLike): Plan;
export declare const unnest: typeof rewriteQuery;
export declare const generalUnnest: typeof rewriteQuery;
export declare const rewrite: typeof rewriteQuery;
export declare function executeUnnested(plan: Plan, contextLike?: ContextLike): QueryResult;
export declare const executeRewritten: typeof executeUnnested;
export declare const runUnnested: typeof executeUnnested;
export declare const runOptimized: typeof executeUnnested;
export declare class UnnestingExecutor {
    private readonly contextLike?;
    constructor(contextLike?: ContextLike | undefined);
    plan(plan: Plan): Plan;
    execute(plan: Plan): QueryResult;
}
export declare function rowsOf(result: {
    rows: readonly Row[];
}): Row[];
export interface ExecutionComparison {
    readonly reference: QueryResult;
    readonly rewritten: QueryResult;
    readonly rewrittenPlan: Plan;
    readonly equal: boolean;
    readonly firstMismatch?: number;
}
export declare function compareExecution(plan: Plan, contextLike?: ContextLike): ExecutionComparison;
export declare const compareNestedAndUnnested: typeof compareExecution;
export declare function assertExecutionEquivalent(plan: Plan, contextLike?: ContextLike): ExecutionComparison;
export declare const assertEquivalentUnnesting: typeof assertExecutionEquivalent;
