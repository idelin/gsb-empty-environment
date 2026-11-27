import type { JsonPathErrorCode } from './errors.ts';

/** SQL/JSON path access mode. */
export type PathMode = 'lax' | 'strict';

/** Kleene three-valued logic truth value used inside filter predicates. */
export type TriState = 'TRUE' | 'FALSE' | 'UNKNOWN';

/** JSON scalar/object/array values (null is an explicit JSON literal). */
export type JsonValue =
  | null
  | boolean
  | string
  | number
  | JsonValue[]
  | { [key: string]: JsonValue };

/** Variables exposed to named-variable references (`$name`). */
export type PathVars = Record<string, JsonValue>;

export type ExprType =
  | 'root'
  | 'current'
  | 'variable'
  | 'literal'
  | 'unary'
  | 'binary'
  | 'exists'
  | 'likeRegex'
  | 'path';

export interface BaseExpr {
  readonly type: ExprType;
  readonly pos: number;
}

export interface RootExpr extends BaseExpr {
  readonly type: 'root';
}

export interface CurrentExpr extends BaseExpr {
  readonly type: 'current';
}

export interface VariableExpr extends BaseExpr {
  readonly type: 'variable';
  readonly name: string;
}

export interface LiteralExpr extends BaseExpr {
  readonly type: 'literal';
  readonly value: JsonValue;
}

export type UnaryOperator = '+' | '-' | '!';

export interface UnaryExpr extends BaseExpr {
  readonly type: 'unary';
  readonly operator: UnaryOperator;
  readonly argument: Expr;
}

export type BinaryOperator =
  | '+'
  | '-'
  | '*'
  | '/'
  | '%'
  | '=='
  | '!='
  | '<'
  | '<='
  | '>'
  | '>='
  | '&&'
  | '||';

export interface BinaryExpr extends BaseExpr {
  readonly type: 'binary';
  readonly operator: BinaryOperator;
  readonly left: Expr;
  readonly right: Expr;
}

export interface ExistsExpr extends BaseExpr {
  readonly type: 'exists';
  readonly argument: PathExpr;
}

export interface LikeRegexExpr extends BaseExpr {
  readonly type: 'likeRegex';
  readonly left: Expr;
  readonly pattern: string;
  readonly flags: string;
}

export interface IndexExpr {
  readonly kind: 'index';
  readonly pos: number;
  /** Arithmetic expression, may reference `last`. */
  readonly value: Expr;
}

export interface SliceExpr {
  readonly kind: 'slice';
  readonly pos: number;
  readonly start: Expr | null;
  readonly end: Expr | null;
}

export interface WildcardIndexExpr {
  readonly kind: 'wildcardIndex';
  readonly pos: number;
}

export type Subscript = IndexExpr | SliceExpr | WildcardIndexExpr;

export type StepType =
  | 'member'
  | 'wildcardMember'
  | 'subscript'
  | 'recursiveDescendant'
  | 'method'
  | 'filter';

export interface MemberStep {
  readonly type: 'member';
  readonly pos: number;
  readonly key: string;
}

export interface WildcardMemberStep {
  readonly type: 'wildcardMember';
  readonly pos: number;
}

export interface SubscriptStep {
  readonly type: 'subscript';
  readonly pos: number;
  readonly subscripts: readonly Subscript[];
}

export interface RecursiveDescendantStep {
  readonly type: 'recursiveDescendant';
  readonly pos: number;
}

export type PathMethod = 'type' | 'size' | 'double';

export interface MethodStep {
  readonly type: 'method';
  readonly pos: number;
  readonly name: PathMethod;
}

export interface FilterStep {
  readonly type: 'filter';
  readonly pos: number;
  readonly predicate: Expr;
}

export type Step =
  | MemberStep
  | WildcardMemberStep
  | SubscriptStep
  | RecursiveDescendantStep
  | MethodStep
  | FilterStep;

export interface PathExpr extends BaseExpr {
  readonly type: 'path';
  readonly base: RootExpr | CurrentExpr | VariableExpr;
  readonly steps: readonly Step[];
}

export type Expr =
  | RootExpr
  | CurrentExpr
  | VariableExpr
  | LiteralExpr
  | UnaryExpr
  | BinaryExpr
  | ExistsExpr
  | LikeRegexExpr
  | PathExpr;

export interface JsonPathAst {
  readonly mode: PathMode;
  readonly expr: PathExpr;
}

/** Re-exported for convenience so consumers only import from the package root. */
export type { JsonPathErrorCode };
