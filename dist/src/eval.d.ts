import { and, col, eqNullSafe, isNotNull, isNull, literal, not, or } from "./ast.js";
import type { ComparisonOperator, ContextLike, Expr, ExecutionContext, Plan, QueryResult, Row, SqlValue, TruthValue } from "./types.js";
export declare class QueryExecutionError extends Error {
    constructor(message: string);
}
export declare class CardinalityViolationError extends QueryExecutionError {
    readonly code = "CARDINALITY_VIOLATION";
    readonly actualRows: number;
    constructor(actualRows: number, detail?: string);
}
export declare class MissingTableError extends QueryExecutionError {
    constructor(name: string);
}
export declare class AmbiguousColumnError extends QueryExecutionError {
    constructor(name: string, matches: readonly string[]);
}
export declare function normalizeContext(context?: ContextLike): ExecutionContext;
export declare function schemaOf(plan: Plan, contextLike?: ContextLike): string[];
export declare function andTruth(left: TruthValue, right: TruthValue): TruthValue;
export declare function orTruth(left: TruthValue, right: TruthValue): TruthValue;
export declare function notTruth(value: TruthValue): TruthValue;
export declare function compareSql(operator: ComparisonOperator, left: SqlValue, right: SqlValue): TruthValue;
export declare function evaluateExpression(expr: Expr, row: Row, contextLike?: ContextLike, outerEnv?: readonly Row[]): SqlValue;
export declare function evaluatePlan(plan: Plan, contextLike?: ContextLike): QueryResult;
export declare const execute: typeof evaluatePlan;
export declare const executeReference: typeof evaluatePlan;
export declare const executeNested: typeof evaluatePlan;
export declare const executeQuery: typeof evaluatePlan;
export declare const executePlan: typeof evaluatePlan;
export declare const runReference: typeof evaluatePlan;
export declare const runNested: typeof evaluatePlan;
export declare const runQuery: typeof evaluatePlan;
export declare class ReferenceExecutor {
    private readonly contextLike?;
    constructor(contextLike?: ContextLike | undefined);
    execute(plan: Plan): QueryResult;
}
export declare function evaluateRows(plan: Plan, contextLike?: ContextLike): Row[];
export declare function walkPlan(plan: Plan, visit: (plan: Plan) => void): void;
export declare function walkExpr(expr: Expr, visit: (expr: Expr) => void): void;
export declare function containsSubquery(plan: Plan): boolean;
export declare function assertFullyDecorrelated(plan: Plan): void;
export declare function isNullValue(value: SqlValue): boolean;
export declare const sqlAnd: typeof and;
export declare const sqlOr: typeof or;
export declare const sqlNot: typeof not;
export declare const sqlIsNull: typeof isNull;
export declare const sqlIsNotNull: typeof isNotNull;
export declare const sqlEq: (left: Expr, right: Expr) => Expr;
export declare const sqlEqNullSafe: typeof eqNullSafe;
export declare const sqlCol: typeof col;
export declare const sqlLiteral: typeof literal;
export declare const sqlTrue = true;
export declare const sqlFalse = false;
export declare const sqlUnknown: null;
