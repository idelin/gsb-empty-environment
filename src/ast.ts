import type {
  AggregateCall,
  AggregateFunction,
  AggregateNode,
  BinaryExpr,
  BinaryOperator,
  CaseExpr,
  CoalesceExpr,
  ColumnRef,
  ComparisonOperator,
  DistinctNode,
  Expr,
  FilterNode,
  GroupKey,
  JoinNode,
  JoinType,
  LimitNode,
  LiteralExpr,
  MarkJoinNode,
  MarkSemantics,
  Plan,
  ProjectColumn,
  ProjectNode,
  RowNumberNode,
  SingleJoinNode,
  SortKey,
  SqlValue,
  SubqueryExpr,
  TableNode,
  UnaryExpr,
  UnaryOperator,
  UnionNode,
  ValuesNode,
} from "./types.js";

export const TRUE = true;
export const FALSE = false;
export const UNKNOWN = null;

export function col(first: string, second?: string): ColumnRef {
  if (second !== undefined) {
    return { kind: "column", relation: first, name: second };
  }
  const dot = first.indexOf(".");
  if (dot > 0 && dot < first.length - 1) {
    return {
      kind: "column",
      relation: first.slice(0, dot),
      name: first.slice(dot + 1),
    };
  }
  return { kind: "column", name: first };
}

export const column = col;

export function literal(value: SqlValue): LiteralExpr {
  return { kind: "literal", value };
}

export const lit = literal;

export function binary(
  op: BinaryOperator,
  left: Expr,
  right: Expr,
): BinaryExpr {
  return { kind: "binary", op, left, right };
}

export function unary(op: UnaryOperator, arg: Expr): UnaryExpr {
  return { kind: "unary", op, arg };
}

export const eq = (left: Expr, right: Expr) => binary("=", left, right);
export const neq = (left: Expr, right: Expr) => binary("<>", left, right);
export const lt = (left: Expr, right: Expr) => binary("<", left, right);
export const lte = (left: Expr, right: Expr) => binary("<=", left, right);
export const gt = (left: Expr, right: Expr) => binary(">", left, right);
export const gte = (left: Expr, right: Expr) => binary(">=", left, right);
export const add = (left: Expr, right: Expr) => binary("+", left, right);
export const sub = (left: Expr, right: Expr) => binary("-", left, right);
export const mul = (left: Expr, right: Expr) => binary("*", left, right);
export const div = (left: Expr, right: Expr) => binary("/", left, right);
export const eqNullSafe = (left: Expr, right: Expr) =>
  binary("is not distinct from", left, right);

export function and(...args: readonly Expr[]): Expr {
  if (args.length === 0) return literal(true);
  return args.slice(1).reduce((left, right) => binary("and", left, right), args[0]);
}

export function or(...args: readonly Expr[]): Expr {
  if (args.length === 0) return literal(false);
  return args.slice(1).reduce((left, right) => binary("or", left, right), args[0]);
}

export const not = (arg: Expr) => unary("not", arg);
export const isNull = (arg: Expr) => unary("is null", arg);
export const isNotNull = (arg: Expr) => unary("is not null", arg);
export const neg = (arg: Expr) => unary("neg", arg);

export function caseWhen(
  whens: readonly { when: Expr; then: Expr }[],
  otherwise?: Expr,
): CaseExpr {
  return { kind: "case", whens, otherwise };
}

export function coalesce(...args: readonly Expr[]): CoalesceExpr {
  return { kind: "coalesce", args };
}

export function call(name: string, ...args: readonly Expr[]): Expr {
  return { kind: "call", name: name.toLowerCase(), args };
}

export function table(name: string, alias?: string): TableNode {
  return alias === undefined
    ? { kind: "table", name }
    : { kind: "table", name, alias };
}

export function values(
  columns: readonly string[],
  rows: readonly Record<string, SqlValue>[],
): ValuesNode {
  return { kind: "values", columns, rows };
}

export function filter(predicate: Expr, input: Plan): FilterNode {
  return { kind: "filter", input, predicate };
}

function isPlan(value: unknown): value is Plan {
  return (
    typeof value === "object" &&
    value !== null &&
    "kind" in value &&
    typeof (value as { kind?: unknown }).kind === "string"
  );
}

export function project(
  first: Plan | readonly ProjectColumn[],
  second: Plan | readonly ProjectColumn[],
): ProjectNode {
  if (isPlan(first) && Array.isArray(second)) {
    return { kind: "project", input: first, columns: second };
  }
  if (Array.isArray(first) && isPlan(second)) {
    return { kind: "project", input: second, columns: first };
  }
  throw new TypeError("project expects a plan and a column list");
}

export function selectColumn(as: string, expr: Expr): ProjectColumn {
  return { as, expr };
}

export function join(
  left: Plan,
  right: Plan,
  condition: Expr = literal(true),
  joinType: JoinType = "inner",
): JoinNode {
  return { kind: "join", joinType, left, right, condition };
}

export function leftJoin(
  left: Plan,
  right: Plan,
  condition: Expr = literal(true),
): JoinNode {
  return join(left, right, condition, "left");
}

export function markJoin(
  left: Plan,
  right: Plan,
  condition: Expr,
  marker: string,
  semantics: MarkSemantics,
): MarkJoinNode {
  return { kind: "markJoin", left, right, condition, marker, semantics };
}

export function singleJoin(
  left: Plan,
  right: Plan,
  condition: Expr = literal(true),
): SingleJoinNode {
  return { kind: "singleJoin", left, right, condition };
}

export function group(as: string, expr: Expr): GroupKey {
  return { as, expr };
}

function normalizeGroupKeys(
  keys: readonly GroupKey[] | readonly Expr[],
): GroupKey[] {
  const out: GroupKey[] = [];
  for (const [index, key] of keys.entries()) {
    if ("kind" in key) {
      const expr = key as Expr;
      const name =
        expr.kind === "column"
          ? expr.name
          : expr.kind === "literal"
            ? `group${index}`
            : `group${index}`;
      out.push({ as: name, expr });
    } else {
      out.push(key as GroupKey);
    }
  }
  return out;
}

export function aggregate(
  input: Plan,
  groupBy: readonly GroupKey[] | readonly Expr[],
  aggregates: readonly AggregateCall[],
): AggregateNode {
  return {
    kind: "aggregate",
    input,
    groupBy: normalizeGroupKeys(groupBy),
    aggregates,
  };
}

export function agg(
  fn: AggregateFunction,
  as: string,
  arg?: Expr,
  distinct = false,
): AggregateCall {
  return arg === undefined
    ? { fn, as, distinct }
    : { fn, as, arg, distinct };
}

export const count = (as = "count", arg?: Expr, distinct = false) =>
  agg("count", as, arg, distinct);
export const sum = (as = "sum", arg?: Expr, distinct = false) =>
  agg("sum", as, arg, distinct);
export const avg = (as = "avg", arg?: Expr, distinct = false) =>
  agg("avg", as, arg, distinct);
export const min = (as = "min", arg?: Expr, distinct = false) =>
  agg("min", as, arg, distinct);
export const max = (as = "max", arg?: Expr, distinct = false) =>
  agg("max", as, arg, distinct);

export function union(left: Plan, right: Plan, all = false): UnionNode {
  return { kind: "union", all, left, right };
}

export function distinct(input: Plan): DistinctNode {
  return { kind: "distinct", input };
}

export function sortKey(
  expr: Expr,
  direction: "asc" | "desc" = "asc",
  nulls: "first" | "last" = "last",
): SortKey {
  return { expr, direction, nulls };
}

export function limit(
  input: Plan,
  countValue: number,
  offset = 0,
  orderBy?: readonly SortKey[],
): LimitNode {
  return orderBy === undefined
    ? { kind: "limit", input, count: countValue, offset }
    : { kind: "limit", input, count: countValue, offset, orderBy };
}

export function rowNumber(
  input: Plan,
  partitionBy: readonly Expr[],
  orderBy: readonly SortKey[],
  as = "__row_number",
): RowNumberNode {
  return { kind: "rowNumber", input, partitionBy, orderBy, as };
}

export function scalar(query: Plan): SubqueryExpr {
  return { kind: "subquery", mode: "scalar", query };
}

export function existsSubquery(query: Plan): SubqueryExpr {
  return { kind: "subquery", mode: "exists", query };
}

export function notExistsSubquery(query: Plan): SubqueryExpr {
  return { kind: "subquery", mode: "exists", query, negated: true };
}

export function inSubquery(left: Expr, query: Plan): SubqueryExpr {
  return { kind: "subquery", mode: "in", query, left, operator: "=" };
}

export function notInSubquery(left: Expr, query: Plan): SubqueryExpr {
  return {
    kind: "subquery",
    mode: "in",
    query,
    left,
    operator: "=",
    negated: true,
  };
}

export function anyCompare(
  left: Expr,
  operator: ComparisonOperator,
  query: Plan,
): SubqueryExpr {
  return { kind: "subquery", mode: "any", query, left, operator };
}

export function allCompare(
  left: Expr,
  operator: ComparisonOperator,
  query: Plan,
): SubqueryExpr {
  return { kind: "subquery", mode: "all", query, left, operator };
}

export const scalarSubquery = scalar;
export const exists = existsSubquery;
export const notExists = notExistsSubquery;
export const inSubqueryExpr = inSubquery;
export const notInSubqueryExpr = notInSubquery;
export const anySubquery = anyCompare;
export const allSubquery = allCompare;
export const isIn = inSubquery;
export const isNotIn = notInSubquery;
export const leftOuterJoin = leftJoin;
export const innerJoin = join;
export const rightJoin = (left: Plan, right: Plan, condition: Expr = literal(true)) =>
  join(left, right, condition, "right");
export const fullJoin = (left: Plan, right: Plan, condition: Expr = literal(true)) =>
  join(left, right, condition, "full");
export const groupBy = group;
export const where = filter;
export const select = project;
export const scan = table;
