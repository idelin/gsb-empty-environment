export type SqlValue = null | boolean | number | string | bigint;

export type Row = Record<string, SqlValue>;

export interface RelationData {
  columns: readonly string[];
  rows: readonly Row[];
}

export type Catalog = Record<string, RelationData | readonly Row[]>;

export interface ExecutionContext {
  readonly catalog?: Catalog;
  readonly tables?: Catalog;
  readonly schema?: Readonly<Record<string, readonly string[]>>;
}

export type ContextLike = ExecutionContext | Catalog;

export type TruthValue = true | false | null;

export type ComparisonOperator =
  | "="
  | "<>"
  | "<"
  | "<="
  | ">"
  | ">=";

export type BinaryOperator =
  | ComparisonOperator
  | "and"
  | "or"
  | "+"
  | "-"
  | "*"
  | "/"
  | "%"
  | "||"
  | "is distinct from"
  | "is not distinct from";

export type UnaryOperator =
  | "not"
  | "neg"
  | "is null"
  | "is not null"
  | "is true"
  | "is false"
  | "is unknown";

export interface ColumnRef {
  readonly kind: "column";
  readonly name: string;
  readonly relation?: string;
}

export interface LiteralExpr {
  readonly kind: "literal";
  readonly value: SqlValue;
}

export interface BinaryExpr {
  readonly kind: "binary";
  readonly op: BinaryOperator;
  readonly left: Expr;
  readonly right: Expr;
}

export interface UnaryExpr {
  readonly kind: "unary";
  readonly op: UnaryOperator;
  readonly arg: Expr;
}

export interface CaseExpr {
  readonly kind: "case";
  readonly whens: readonly { readonly when: Expr; readonly then: Expr }[];
  readonly otherwise?: Expr;
}

export interface CoalesceExpr {
  readonly kind: "coalesce";
  readonly args: readonly Expr[];
}

export interface CallExpr {
  readonly kind: "call";
  readonly name: string;
  readonly args: readonly Expr[];
}

export interface SubqueryExpr {
  readonly kind: "subquery";
  readonly mode: "scalar" | "exists" | "in" | "any" | "all";
  readonly query: Plan;
  readonly left?: Expr;
  readonly operator?: ComparisonOperator;
  readonly negated?: boolean;
}

export type Expr =
  | ColumnRef
  | LiteralExpr
  | BinaryExpr
  | UnaryExpr
  | CaseExpr
  | CoalesceExpr
  | CallExpr
  | SubqueryExpr;

export interface TableNode {
  readonly kind: "table";
  readonly name: string;
  readonly alias?: string;
  readonly columns?: readonly string[];
  readonly rows?: readonly Row[];
}

export interface ValuesNode {
  readonly kind: "values";
  readonly columns: readonly string[];
  readonly rows: readonly Row[];
}

export interface FilterNode {
  readonly kind: "filter";
  readonly input: Plan;
  readonly predicate: Expr;
}

export interface ProjectColumn {
  readonly as: string;
  readonly expr: Expr;
}

export interface ProjectNode {
  readonly kind: "project";
  readonly input: Plan;
  readonly columns: readonly ProjectColumn[];
}

export type JoinType = "inner" | "left" | "right" | "full";

export interface JoinNode {
  readonly kind: "join";
  readonly joinType: JoinType;
  readonly left: Plan;
  readonly right: Plan;
  readonly condition: Expr;
}

export interface MarkSemantics {
  readonly quantifier: "exists" | "any" | "all";
  readonly operator?: ComparisonOperator;
  readonly left?: Expr;
  readonly right?: Expr;
  readonly negated?: boolean;
}

export interface MarkJoinNode {
  readonly kind: "markJoin";
  readonly left: Plan;
  readonly right: Plan;
  readonly condition: Expr;
  readonly marker: string;
  readonly semantics: MarkSemantics;
}

export interface SingleJoinNode {
  readonly kind: "singleJoin";
  readonly left: Plan;
  readonly right: Plan;
  readonly condition: Expr;
}

export interface GroupKey {
  readonly as: string;
  readonly expr: Expr;
}

export type AggregateFunction = "count" | "sum" | "avg" | "min" | "max";

export interface AggregateCall {
  readonly fn: AggregateFunction;
  readonly as: string;
  readonly arg?: Expr;
  readonly distinct?: boolean;
}

export interface AggregateNode {
  readonly kind: "aggregate";
  readonly input: Plan;
  readonly groupBy: readonly GroupKey[];
  readonly aggregates: readonly AggregateCall[];
}

export interface UnionNode {
  readonly kind: "union";
  readonly all: boolean;
  readonly left: Plan;
  readonly right: Plan;
}

export interface DistinctNode {
  readonly kind: "distinct";
  readonly input: Plan;
}

export interface SortKey {
  readonly expr: Expr;
  readonly direction?: "asc" | "desc";
  readonly nulls?: "first" | "last";
}

export interface LimitNode {
  readonly kind: "limit";
  readonly input: Plan;
  readonly count: number;
  readonly offset?: number;
  readonly orderBy?: readonly SortKey[];
}

export interface RowNumberNode {
  readonly kind: "rowNumber";
  readonly input: Plan;
  readonly partitionBy: readonly Expr[];
  readonly orderBy: readonly SortKey[];
  readonly as: string;
}

export type Plan =
  | TableNode
  | ValuesNode
  | FilterNode
  | ProjectNode
  | JoinNode
  | MarkJoinNode
  | SingleJoinNode
  | AggregateNode
  | UnionNode
  | DistinctNode
  | LimitNode
  | RowNumberNode;

export interface QueryResult {
  readonly columns: readonly string[];
  readonly rows: readonly Row[];
}

export type RelExpr = Plan;
export type Query = Plan;
export type PlanNode = Plan;
export type ExprNode = Expr;
export type Value = SqlValue;
