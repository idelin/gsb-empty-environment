/**
 * Query AST node definitions and builders.
 *
 * Values enter a query only through `Param` nodes. A `Param` is bound by
 * node identity (object reference): two nodes holding equal values are two
 * distinct binding sites, while the same node referenced from several
 * places in the AST is a single binding site.
 */

export class Param<T = unknown> {
  readonly kind = 'param';
  readonly value: T;
  constructor(value: T) {
    this.value = value;
  }
}

export interface Ident {
  readonly kind: 'ident';
  /** Qualified name, e.g. ['users', 'id'] renders as "users"."id". */
  readonly path: readonly string[];
}

/**
 * A static SQL fragment. Raw text is emitted verbatim and can never carry
 * values: the renderer rejects fragments containing placeholder syntax.
 */
export interface Raw {
  readonly kind: 'raw';
  readonly text: string;
}

export type CmpOp = '=' | '<>' | '<' | '<=' | '>' | '>=' | 'LIKE';

export interface Cmp {
  readonly kind: 'cmp';
  readonly op: CmpOp;
  readonly left: Expr;
  readonly right: Expr;
}

export interface InList {
  readonly kind: 'in';
  readonly item: Expr;
  readonly list: readonly Expr[];
  readonly negated: boolean;
}

export interface InSubquery {
  readonly kind: 'inSub';
  readonly item: Expr;
  readonly query: Select;
  readonly negated: boolean;
}

export interface Logical {
  readonly kind: 'and' | 'or';
  readonly items: readonly Expr[];
}

export interface Not {
  readonly kind: 'not';
  readonly item: Expr;
}

export interface ScalarSubquery {
  readonly kind: 'scalarSub';
  readonly query: Select;
}

export interface Exists {
  readonly kind: 'exists';
  readonly query: Select;
  readonly negated: boolean;
}

export type Expr =
  | Param
  | Ident
  | Raw
  | Cmp
  | InList
  | InSubquery
  | Logical
  | Not
  | ScalarSubquery
  | Exists;

export interface Column {
  readonly expr: Expr;
  readonly alias: string | null;
}

export type FromSource =
  | { readonly kind: 'table'; readonly table: Ident; readonly alias: string | null }
  | { readonly kind: 'subquery'; readonly query: Select; readonly alias: string };

export interface OrderItem {
  readonly expr: Expr;
  readonly dir: 'ASC' | 'DESC';
}

export interface Select {
  readonly kind: 'select';
  readonly columns: readonly Column[];
  readonly from: readonly FromSource[];
  readonly where: Expr | null;
  readonly orderBy: readonly OrderItem[];
  readonly limit: Param | null;
  readonly offset: Param | null;
}

// ---------------------------------------------------------------------------
// Builders
// ---------------------------------------------------------------------------

export function param<T>(value: T): Param<T> {
  return new Param(value);
}

/** A (possibly qualified) identifier: ident('users', 'id'). */
export function ident(...path: string[]): Ident {
  if (path.length === 0 || path.some((p) => p.length === 0)) {
    throw new Error('ident() requires a non-empty path of non-empty names');
  }
  return { kind: 'ident', path };
}

/** Convenience: field('users.id') === ident('users', 'id'). */
export function field(name: string): Ident {
  return ident(...name.split('.'));
}

export function raw(text: string): Raw {
  return { kind: 'raw', text };
}

export function cmp(op: CmpOp, left: Expr, right: Expr): Cmp {
  return { kind: 'cmp', op, left, right };
}

export const eq = (l: Expr, r: Expr): Cmp => cmp('=', l, r);
export const neq = (l: Expr, r: Expr): Cmp => cmp('<>', l, r);
export const lt = (l: Expr, r: Expr): Cmp => cmp('<', l, r);
export const lte = (l: Expr, r: Expr): Cmp => cmp('<=', l, r);
export const gt = (l: Expr, r: Expr): Cmp => cmp('>', l, r);
export const gte = (l: Expr, r: Expr): Cmp => cmp('>=', l, r);
export const like = (l: Expr, r: Expr): Cmp => cmp('LIKE', l, r);

export function inList(item: Expr, list: readonly Expr[]): InList {
  return { kind: 'in', item, list, negated: false };
}

export function notInList(item: Expr, list: readonly Expr[]): InList {
  return { kind: 'in', item, list, negated: true };
}

export function inSubquery(item: Expr, query: Select): InSubquery {
  return { kind: 'inSub', item, query, negated: false };
}

export function notInSubquery(item: Expr, query: Select): InSubquery {
  return { kind: 'inSub', item, query, negated: true };
}

export function and(...items: Expr[]): Logical {
  return { kind: 'and', items };
}

export function or(...items: Expr[]): Logical {
  return { kind: 'or', items };
}

export function not(item: Expr): Not {
  return { kind: 'not', item };
}

export function scalar(query: Select): ScalarSubquery {
  return { kind: 'scalarSub', query };
}

export function exists(query: Select): Exists {
  return { kind: 'exists', query, negated: false };
}

export function notExists(query: Select): Exists {
  return { kind: 'exists', query, negated: true };
}

export interface SelectInit {
  columns?: readonly (Expr | Column)[];
  from?: readonly (Ident | FromSource)[];
  where?: Expr;
  orderBy?: readonly (OrderItem | Expr)[];
  limit?: Param;
  offset?: Param;
}

export function select(init: SelectInit): Select {
  const columns: Column[] = (init.columns ?? []).map((c) =>
    isColumn(c) ? c : { expr: c as Expr, alias: null },
  );
  const from: FromSource[] = (init.from ?? []).map((f) =>
    (f as Ident).kind === 'ident'
      ? { kind: 'table', table: f as Ident, alias: null }
      : (f as FromSource),
  );
  const orderBy: OrderItem[] = (init.orderBy ?? []).map((o) =>
    isOrderItem(o) ? o : { expr: o as Expr, dir: 'ASC' },
  );
  return {
    kind: 'select',
    columns,
    from,
    where: init.where ?? null,
    orderBy,
    limit: init.limit ?? null,
    offset: init.offset ?? null,
  };
}

export function col(expr: Expr, alias: string): Column {
  return { expr, alias };
}

export function fromSub(query: Select, alias: string): FromSource {
  return { kind: 'subquery', query, alias };
}

export function fromTable(table: Ident, alias: string): FromSource {
  return { kind: 'table', table, alias };
}

function isColumn(node: object): boolean {
  return (
    (node as Column).expr !== undefined &&
    (node as { kind?: string }).kind === undefined
  );
}

function isOrderItem(node: object): boolean {
  return (
    (node as OrderItem).dir !== undefined &&
    (node as OrderItem).expr !== undefined
  );
}
