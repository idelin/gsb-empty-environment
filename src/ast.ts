import { assertIdentifier } from './ident.ts';

export type ParamType = 'string' | 'integer' | 'number' | 'boolean' | 'date' | 'null';
export type CmpOp = '=' | '<>' | '<' | '>' | '<=' | '>=' | 'LIKE';
export type JoinType = 'inner' | 'left' | 'right' | 'full';
export type Direction = 'asc' | 'desc';

const PARAM_TYPES: ReadonlySet<string> = new Set([
  'string',
  'integer',
  'number',
  'boolean',
  'date',
  'null',
]);

const CMP_OPS: ReadonlySet<string> = new Set(['=', '<>', '<', '>', '<=', '>=', 'LIKE']);
const JOIN_TYPES: ReadonlySet<string> = new Set(['inner', 'left', 'right', 'full']);

export const ALLOWED_FUNCTIONS: ReadonlySet<string> = new Set([
  'COUNT',
  'SUM',
  'AVG',
  'MIN',
  'MAX',
  'COALESCE',
  'LENGTH',
  'LOWER',
  'UPPER',
]);

export const MAX_ORDER_ITEMS = 10;
export const MAX_LIMIT = 10_000;

export interface ParamNode {
  kind: 'param';
  name: string;
  paramType: ParamType;
}

export interface LitNode {
  kind: 'lit';
  value: number | boolean | null;
}

export interface ColNode {
  kind: 'col';
  qualifier?: string;
  name: string;
}

export interface BoolNode {
  kind: 'bool';
  value: boolean;
}

export interface BinNode {
  kind: 'bin';
  op: CmpOp;
  left: Expr;
  right: Expr;
}

export interface UnNode {
  kind: 'un';
  op: 'NOT' | '-';
  expr: Expr;
}

export interface IsNullNode {
  kind: 'isNull';
  expr: Expr;
}

export interface InListNode {
  kind: 'inList';
  expr: Expr;
  items: ParamNode[];
  negated: boolean;
}

export interface InQueryNode {
  kind: 'inQuery';
  expr: Expr;
  query: SelectNode;
  negated: boolean;
}

export interface ExistsNode {
  kind: 'exists';
  query: SelectNode;
}

export interface ScalarQueryNode {
  kind: 'scalarQuery';
  query: SelectNode;
}

export interface FuncNode {
  kind: 'func';
  name: string;
  args: Expr[];
  star: boolean;
  distinct: boolean;
}

export interface BetweenNode {
  kind: 'between';
  expr: Expr;
  low: Expr;
  high: Expr;
  negated: boolean;
}

export type Expr =
  | ParamNode
  | LitNode
  | ColNode
  | BoolNode
  | BinNode
  | UnNode
  | IsNullNode
  | InListNode
  | InQueryNode
  | ExistsNode
  | ScalarQueryNode
  | FuncNode
  | BetweenNode;

export interface StarItem {
  star: true;
  qualifier?: string;
}

export interface ExprItem {
  star?: false;
  expr: Expr;
  as?: string;
}

export type SelectItem = StarItem | ExprItem;

export interface TableRelation {
  kind: 'table';
  schema?: string;
  name: string;
  alias?: string;
}

export interface SubqueryRelation {
  kind: 'subquery';
  query: SelectNode;
  alias: string;
}

export type Relation = TableRelation | SubqueryRelation;

export interface JoinNode {
  type: JoinType;
  relation: Relation;
  on: Expr;
}

export interface OrderItem {
  expr: Expr;
  direction: Direction;
}

export interface SelectNode {
  kind: 'select';
  distinct: boolean;
  selectList: SelectItem[];
  from: Relation;
  joins: JoinNode[];
  where: Expr | null;
  groupBy: Expr[];
  having: Expr | null;
  orderBy: OrderItem[];
  limit: number | null;
}

export interface SelectConfig {
  distinct?: boolean;
  selectList: SelectItem[];
  from: Relation;
  joins?: JoinNode[];
  orderBy?: OrderItem[];
  groupBy?: Expr[];
  where?: Expr | null;
  having?: Expr | null;
  limit?: number | null;
}

export function param(name: string, paramType: ParamType): ParamNode {
  assertIdentifier(name, 'parameter');
  if (!PARAM_TYPES.has(paramType)) {
    throw new TypeError(`unknown parameter type: ${String(paramType)}`);
  }
  return { kind: 'param', name, paramType };
}

export function lit(value: number | boolean | null): LitNode {
  if (value === null || typeof value === 'boolean') {
    return { kind: 'lit', value };
  }
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    throw new TypeError('literal must be a finite number, boolean or null');
  }
  return { kind: 'lit', value };
}

export function col(qualifierOrName: string, name?: string): ColNode {
  if (name === undefined) {
    assertIdentifier(qualifierOrName, 'column');
    return { kind: 'col', name: qualifierOrName };
  }
  assertIdentifier(qualifierOrName, 'table qualifier');
  assertIdentifier(name, 'column');
  return { kind: 'col', qualifier: qualifierOrName, name };
}

export function bool(value: boolean): BoolNode {
  return { kind: 'bool', value };
}

export function cmp(op: CmpOp, left: Expr, right: Expr): BinNode {
  if (!CMP_OPS.has(op)) {
    throw new TypeError(`unsupported comparison operator: ${String(op)}`);
  }
  return { kind: 'bin', op, left, right };
}

export function and(...exprs: Expr[]): Expr {
  if (exprs.length === 0) {
    throw new TypeError('and() requires at least one expression');
  }
  return exprs.reduce((left, right) => ({ kind: 'bin', op: 'AND' as const, left, right }));
}

export function or(...exprs: Expr[]): Expr {
  if (exprs.length === 0) {
    throw new TypeError('or() requires at least one expression');
  }
  return exprs.reduce((left, right) => ({ kind: 'bin', op: 'OR' as const, left, right }));
}

export function not(expr: Expr): UnNode {
  return { kind: 'un', op: 'NOT', expr };
}

export function isNull(expr: Expr): IsNullNode {
  return { kind: 'isNull', expr };
}

export function inList(expr: Expr, items: ParamNode[], negated = false): InListNode {
  for (const item of items) {
    if (item.kind !== 'param') {
      throw new TypeError('IN lists accept only typed param() nodes');
    }
  }
  return { kind: 'inList', expr, items: [...items], negated };
}

export function inQuery(expr: Expr, query: SelectNode, negated = false): InQueryNode {
  return { kind: 'inQuery', expr, query, negated };
}

export function exists(query: SelectNode): ExistsNode {
  return { kind: 'exists', query };
}

export function scalar(query: SelectNode): ScalarQueryNode {
  return { kind: 'scalarQuery', query };
}

export function between(expr: Expr, low: Expr, high: Expr, negated = false): BetweenNode {
  return { kind: 'between', expr, low, high, negated };
}

export function fn(name: string, args: Expr[], options: { distinct?: boolean } = {}): FuncNode {
  const upper = name.toUpperCase();
  if (!ALLOWED_FUNCTIONS.has(upper)) {
    throw new TypeError(`function not allowed: ${name}`);
  }
  return {
    kind: 'func',
    name: upper,
    args: [...args],
    star: false,
    distinct: options.distinct === true,
  };
}

export function countStar(): FuncNode {
  return { kind: 'func', name: 'COUNT', args: [], star: true, distinct: false };
}

export function table(name: string, alias?: string, schema?: string): TableRelation {
  assertIdentifier(name, 'table');
  if (alias !== undefined) {
    assertIdentifier(alias, 'table alias');
  }
  if (schema !== undefined) {
    assertIdentifier(schema, 'schema');
  }
  return schema === undefined
    ? { kind: 'table', name, ...(alias === undefined ? {} : { alias }) }
    : { kind: 'table', schema, name, ...(alias === undefined ? {} : { alias }) };
}

export function subquery(query: SelectNode, alias: string): SubqueryRelation {
  assertIdentifier(alias, 'subquery alias');
  return { kind: 'subquery', query, alias };
}

export function join(type: JoinType, relation: Relation, on: Expr): JoinNode {
  if (!JOIN_TYPES.has(type)) {
    throw new TypeError(`unsupported join type: ${String(type)}`);
  }
  return { type, relation, on };
}

export function asc(expr: Expr): OrderItem {
  return { expr, direction: 'asc' };
}

export function desc(expr: Expr): OrderItem {
  return { expr, direction: 'desc' };
}

export function star(qualifier?: string): StarItem {
  if (qualifier !== undefined) {
    assertIdentifier(qualifier, 'star qualifier');
  }
  return qualifier === undefined ? { star: true } : { star: true, qualifier };
}

export function select(config: SelectConfig): SelectNode {
  if (!Array.isArray(config.selectList) || config.selectList.length === 0) {
    throw new TypeError('select() requires a non-empty select list');
  }
  const selectList = config.selectList.map((item): SelectItem => {
    if (item !== null && typeof item === 'object' && 'kind' in item) {
      return { expr: item as Expr };
    }
    return item;
  });
  if (config.limit !== undefined && config.limit !== null) {
    if (!Number.isSafeInteger(config.limit) || config.limit < 0 || config.limit > MAX_LIMIT) {
      throw new TypeError(`limit must be an integer between 0 and ${MAX_LIMIT}`);
    }
  }
  return {
    kind: 'select',
    distinct: config.distinct === true,
    selectList,
    from: config.from,
    joins: config.joins ?? [],
    where: config.where ?? null,
    groupBy: config.groupBy ?? [],
    having: config.having ?? null,
    orderBy: config.orderBy ?? [],
    limit: config.limit ?? null,
  };
}

export function walkExpr(expr: Expr, visit: (node: Expr) => void): void {
  visit(expr);
  switch (expr.kind) {
    case 'bin':
      walkExpr(expr.left, visit);
      walkExpr(expr.right, visit);
      return;
    case 'un':
    case 'isNull':
      walkExpr(expr.expr, visit);
      return;
    case 'inList':
      walkExpr(expr.expr, visit);
      expr.items.forEach((item) => walkExpr(item, visit));
      return;
    case 'inQuery':
      walkExpr(expr.expr, visit);
      return;
    case 'between':
      walkExpr(expr.expr, visit);
      walkExpr(expr.low, visit);
      walkExpr(expr.high, visit);
      return;
    case 'func':
      expr.args.forEach((arg) => walkExpr(arg, visit));
      return;
    case 'exists':
    case 'scalarQuery':
      return;
    case 'param':
    case 'lit':
    case 'col':
    case 'bool':
      return;
  }
  const exhaustive: never = expr;
  throw new Error(`unsupported expression: ${String(exhaustive)}`);
}
