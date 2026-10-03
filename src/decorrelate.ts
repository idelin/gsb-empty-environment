/**
 * Subquery unnesting / decorrelation.
 *
 * Rewrites correlated (and uncorrelated) EXISTS / NOT EXISTS / IN / NOT IN
 * subqueries appearing as top-level conjuncts of the outer WHERE clause into
 * SEMI / ANTI joins, eliminating the per-row re-execution of the subquery.
 *
 * Safety rules (a conjunct is left untouched when any rule fails):
 *   - outer query must have exactly one table in FROM;
 *   - subquery must have exactly one table in FROM and no nested subqueries;
 *   - correlated predicates must be equalities (inner.col = outer.expr);
 *   - predicates referencing only the inner table become filters on the
 *     inner (build) side;
 *   - anything else (OR-ed subqueries, non-equality correlation, mixed
 *     predicates) stays as a residual correlated predicate.
 */

import {
  BinaryExpr,
  ColumnExpr,
  Expr,
  SelectStmt,
} from './ast';

export type JoinType = 'SEMI' | 'ANTI';

export interface JoinKey {
  outer: string;
  inner: string;
}

export interface SemiJoinSpec {
  type: JoinType;
  source: 'EXISTS' | 'NOT EXISTS' | 'IN' | 'NOT IN';
  table: string;
  alias: string;
  keys: JoinKey[];
  filters: Expr[];
}

export interface RewriteResult {
  changed: boolean;
  query: SelectStmt;
  outer: { table: string; alias: string } | null;
  joins: SemiJoinSpec[];
  residual: Expr[];
  notes: string[];
}

export function splitConjuncts(expr: Expr | null): Expr[] {
  if (!expr) return [];
  if (expr.kind === 'and') {
    const out: Expr[] = [];
    for (const item of expr.items) out.push(...splitConjuncts(item));
    return out;
  }
  return [expr];
}

export function collectColumns(expr: Expr, out: ColumnExpr[] = []): ColumnExpr[] {
  switch (expr.kind) {
    case 'column':
      out.push(expr);
      break;
    case 'binary':
      collectColumns(expr.left, out);
      collectColumns(expr.right, out);
      break;
    case 'and':
    case 'or':
      for (const item of expr.items) collectColumns(item, out);
      break;
    case 'in':
      collectColumns(expr.expr, out);
      break;
    default:
      break;
  }
  return out;
}

function containsSubquery(expr: Expr | null): boolean {
  if (!expr) return false;
  switch (expr.kind) {
    case 'exists':
    case 'in':
      return true;
    case 'binary':
      return containsSubquery(expr.left) || containsSubquery(expr.right);
    case 'and':
    case 'or':
      return expr.items.some(containsSubquery);
    default:
      return false;
  }
}

type Scope = 'inner' | 'outer' | 'none' | 'mixed';

function classify(expr: Expr, innerAlias: string, outerAlias: string): Scope {
  const cols = collectColumns(expr);
  if (cols.length === 0) return 'none';
  let scope: Scope = 'none';
  for (const col of cols) {
    let s: Scope;
    if (col.table === null || col.table === innerAlias) s = 'inner';
    else if (col.table === outerAlias) s = 'outer';
    else s = 'mixed';
    if (scope === 'none') scope = s;
    else if (scope !== s) return 'mixed';
  }
  return scope;
}

function isEquality(e: Expr): e is BinaryExpr {
  return e.kind === 'binary' && e.op === '=';
}

interface UnnestingOk {
  ok: true;
  keys: JoinKey[];
  filters: Expr[];
}

interface UnnestingFail {
  ok: false;
  reason: string;
}

type Unnesting = UnnestingOk | UnnestingFail;

function fail(reason: string): UnnestingFail {
  return { ok: false, reason };
}

/** Split a subquery's WHERE clause into join keys and inner-local filters. */
function extractKeysAndFilters(
  sub: SelectStmt,
  outerAlias: string,
  extraKeys: JoinKey[],
): Unnesting {
  if (sub.from.length !== 1) {
    return fail('subquery FROM must reference exactly one table');
  }
  if (containsSubquery(sub.where)) {
    return fail('nested subqueries are not supported');
  }
  const innerAlias = sub.from[0].alias;
  const keys: JoinKey[] = [...extraKeys];
  const filters: Expr[] = [];
  for (const conjunct of splitConjuncts(sub.where)) {
    if (isEquality(conjunct)) {
      const l = classify(conjunct.left, innerAlias, outerAlias);
      const r = classify(conjunct.right, innerAlias, outerAlias);
      if (l === 'inner' && r === 'outer') {
        keys.push({ inner: exprToSql(conjunct.left), outer: exprToSql(conjunct.right) });
        continue;
      }
      if (l === 'outer' && r === 'inner') {
        keys.push({ inner: exprToSql(conjunct.right), outer: exprToSql(conjunct.left) });
        continue;
      }
    }
    const scope = classify(conjunct, innerAlias, outerAlias);
    if (scope === 'inner' || scope === 'none') {
      filters.push(conjunct);
      continue;
    }
    return fail(`cannot decorrelate predicate: ${exprToSql(conjunct)}`);
  }
  return { ok: true, keys, filters };
}

export function decorrelate(stmt: SelectStmt): RewriteResult {
  const notes: string[] = [];
  const joins: SemiJoinSpec[] = [];
  const residual: Expr[] = [];

  if (stmt.from.length !== 1) {
    return {
      changed: false,
      query: stmt,
      outer: null,
      joins,
      residual: splitConjuncts(stmt.where),
      notes: ['outer query must reference exactly one table; nothing rewritten'],
    };
  }
  const outer = stmt.from[0];

  for (const conjunct of splitConjuncts(stmt.where)) {
    if (conjunct.kind !== 'exists' && conjunct.kind !== 'in') {
      residual.push(conjunct);
      continue;
    }
    const sub = conjunct.subquery;
    let extraKeys: JoinKey[] = [];
    if (conjunct.kind === 'in') {
      const items = sub.select;
      if (items.length !== 1 || items[0].kind !== 'expr') {
        residual.push(conjunct);
        notes.push('IN subquery must select exactly one expression; kept as-is');
        continue;
      }
      extraKeys = [
        { outer: exprToSql(conjunct.expr), inner: exprToSql(items[0].expr) },
      ];
    }
    const result = extractKeysAndFilters(sub, outer.alias, extraKeys);
    if (!result.ok) {
      residual.push(conjunct);
      notes.push(`${result.reason}; kept as-is`);
      continue;
    }
    const source =
      conjunct.kind === 'exists'
        ? conjunct.negated
          ? 'NOT EXISTS'
          : 'EXISTS'
        : conjunct.negated
          ? 'NOT IN'
          : 'IN';
    joins.push({
      type: conjunct.negated ? 'ANTI' : 'SEMI',
      source,
      table: sub.from[0].table,
      alias: sub.from[0].alias,
      keys: result.keys,
      filters: result.filters,
    });
    notes.push(
      `${source} subquery on ${sub.from[0].table} ${sub.from[0].alias} ` +
        `rewritten as ${conjunct.negated ? 'ANTI' : 'SEMI'} JOIN ` +
        `(keys: ${result.keys.map((k) => `${k.outer} = ${k.inner}`).join(', ') || 'none'})`,
    );
  }

  return { changed: joins.length > 0, query: stmt, outer, joins, residual, notes };
}

export function exprToSql(expr: Expr): string {
  switch (expr.kind) {
    case 'column':
      return expr.table ? `${expr.table}.${expr.name}` : expr.name;
    case 'literal':
      if (expr.value === null) return 'NULL';
      return typeof expr.value === 'string' ? `'${expr.value}'` : String(expr.value);
    case 'binary':
      return `${exprToSql(expr.left)} ${expr.op} ${exprToSql(expr.right)}`;
    case 'and':
      return expr.items.map(exprToSql).join(' AND ');
    case 'or':
      return expr.items.map((i) => `(${exprToSql(i)})`).join(' OR ');
    case 'exists':
      return `${expr.negated ? 'NOT ' : ''}EXISTS (${selectToSql(expr.subquery)})`;
    case 'in':
      return `${exprToSql(expr.expr)} ${expr.negated ? 'NOT ' : ''}IN (${selectToSql(expr.subquery)})`;
  }
}

export function selectToSql(stmt: SelectStmt): string {
  const select = stmt.select
    .map((item) => {
      if (item.kind === 'star') return item.table ? `${item.table}.*` : '*';
      const s = exprToSql(item.expr);
      return item.alias ? `${s} AS ${item.alias}` : s;
    })
    .join(', ');
  const from = stmt.from.map((t) => `${t.table} ${t.alias}`).join(', ');
  const where = stmt.where ? ` WHERE ${exprToSql(stmt.where)}` : '';
  return `SELECT ${select} FROM ${from}${where}`;
}

/** Render the decorrelated query using explicit SEMI/ANTI JOIN syntax. */
export function renderRewrittenSql(result: RewriteResult): string {
  if (!result.outer) return selectToSql(result.query);
  const outer = result.outer;
  const select = result.query.select
    .map((item) => {
      if (item.kind === 'star') return item.table ? `${item.table}.*` : `${outer.alias}.*`;
      const s = exprToSql(item.expr);
      return item.alias ? `${s} AS ${item.alias}` : s;
    })
    .join(', ');
  let sql = `SELECT ${select} FROM ${outer.table} ${outer.alias}`;
  for (const join of result.joins) {
    const onParts = [
      ...join.keys.map((k) => `${k.outer} = ${k.inner}`),
      ...join.filters.map(exprToSql),
    ];
    sql += ` ${join.type} JOIN ${join.table} ${join.alias}`;
    if (onParts.length > 0) sql += ` ON ${onParts.join(' AND ')}`;
  }
  if (result.residual.length > 0) {
    sql += ` WHERE ${result.residual.map(exprToSql).join(' AND ')}`;
  }
  return sql;
}
