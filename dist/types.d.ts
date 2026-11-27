export type JsonPrimitive = string | number | boolean | null;
export type JsonValue = JsonPrimitive | JsonValue[] | {
    [key: string]: JsonValue;
};
export type JsonPathMode = "lax" | "strict";
export type TruthValue = true | false | "unknown";
export type PathExpressionOrigin = "$" | "@";
export type IndexExpression = {
    kind: "integer";
    value: number;
} | {
    kind: "last";
} | {
    kind: "unary";
    operator: "+" | "-";
    operand: IndexExpression;
} | {
    kind: "binary";
    operator: "+" | "-" | "*" | "/" | "%";
    left: IndexExpression;
    right: IndexExpression;
};
export type ArrayIndexSelector = {
    kind: "index";
    expression: IndexExpression;
} | {
    kind: "range";
    from: IndexExpression;
    to: IndexExpression;
};
export interface MemberStep {
    kind: "member";
    key: string;
    offset: number;
}
export interface MemberWildcardStep {
    kind: "memberWildcard";
    offset: number;
}
export interface ArrayAccessStep {
    kind: "arrayAccess";
    selectors: ArrayIndexSelector[];
    wildcard: boolean;
    offset: number;
}
export interface RecursiveStep {
    kind: "recursive";
    offset: number;
}
export interface MethodStep {
    kind: "method";
    name: string;
    args: LiteralExpression[];
    offset: number;
}
export interface FilterStep {
    kind: "filter";
    predicate: BooleanExpression;
    offset: number;
}
export type PathStep = MemberStep | MemberWildcardStep | ArrayAccessStep | RecursiveStep | MethodStep | FilterStep;
export interface PathExpression {
    kind: "path";
    origin: PathExpressionOrigin;
    steps: PathStep[];
    offset: number;
}
export interface LiteralExpression {
    kind: "literal";
    value: JsonValue;
    offset: number;
}
export interface UnaryExpression {
    kind: "unary";
    operator: "+" | "-" | "!";
    operand: Expression;
    offset: number;
}
export interface BinaryExpression {
    kind: "binary";
    operator: "+" | "-" | "*" | "/" | "%" | "&&" | "||" | "==" | "!=" | "<" | "<=" | ">" | ">=";
    left: Expression;
    right: Expression;
    offset: number;
}
export interface ExistsExpression {
    kind: "exists";
    path: PathExpression;
    offset: number;
}
export interface LikeRegexExpression {
    kind: "likeRegex";
    left: Expression;
    pattern: string;
    flags: string;
    offset: number;
}
export interface LastExpression {
    kind: "last";
    offset: number;
}
export type Expression = PathExpression | LiteralExpression | UnaryExpression | BinaryExpression | ExistsExpression | LikeRegexExpression | LastExpression;
export type BooleanExpression = Expression;
export interface JsonPath {
    kind: "jsonpath";
    mode: JsonPathMode;
    modeDeclared: boolean;
    root: PathExpression;
    source: string;
}
export interface ParseOptions {
    defaultMode?: JsonPathMode;
}
export interface EvaluateOptions {
    defaultMode?: JsonPathMode;
    maxDepth?: number;
}
export interface EvaluationSuccess {
    ok: true;
    mode: JsonPathMode;
    values: JsonValue[];
}
export interface EvaluationFailure {
    ok: false;
    mode: JsonPathMode;
    values: [];
    error: JsonPathEvaluationErrorLike;
}
export type EvaluationResult = EvaluationSuccess | EvaluationFailure;
export interface JsonPathEvaluationErrorLike extends Error {
    code: JsonPathErrorCode;
    mode?: JsonPathMode;
    offset?: number;
}
export type JsonPathErrorCode = "PARSE_ERROR" | "MEMBER_ON_NON_OBJECT" | "MISSING_KEY" | "INDEX_ON_NON_ARRAY" | "INDEX_OUT_OF_BOUNDS" | "WILDCARD_ON_SCALAR" | "METHOD_ON_INVALID_TYPE" | "TYPE_ERROR" | "ARITHMETIC_ERROR" | "INVALID_REGEX" | "DEPTH_LIMIT";
export type IndexPathSegment = {
    kind: "key";
    key: string;
} | {
    kind: "index";
    index: number;
} | {
    kind: "indexRange";
    from: number;
    to: number;
} | {
    kind: "arraySelection";
    wildcard: boolean;
    selectors: readonly ArrayIndexSelector[];
    wrapScalar?: boolean;
} | {
    kind: "anyIndex";
} | {
    kind: "anyKey";
} | {
    kind: "laxUnwrap";
} | {
    kind: "recursive";
};
export interface IndexTerm {
    readonly test: "exists" | "equals";
    readonly path: readonly IndexPathSegment[];
    readonly value?: JsonValue;
    readonly source?: string;
}
export type IndexPlan = {
    readonly kind: "all";
    readonly reason?: string;
} | {
    readonly kind: "term";
    readonly term: IndexTerm;
} | {
    readonly kind: "and";
    readonly children: readonly IndexPlan[];
} | {
    readonly kind: "or";
    readonly children: readonly IndexPlan[];
};
export interface IndexExtractionOptions {
    mode?: JsonPathMode;
}
export type ContainmentQuery = {
    readonly kind: "true";
} | {
    readonly kind: "contains";
    readonly value: JsonValue;
} | {
    readonly kind: "and";
    readonly children: readonly ContainmentQuery[];
} | {
    readonly kind: "or";
    readonly children: readonly ContainmentQuery[];
};
export type PathInput = string | JsonPath;
//# sourceMappingURL=types.d.ts.map