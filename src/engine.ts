/**
 * Tiny in-memory execution engine used to demonstrate and verify the rewrite.
 *
 *  - executeBaseline: naive correlated evaluation. The subquery is
 *    re-executed for every outer row (the performance trap).
 *  - executePlan: runs the rewritten physical plan. HashSemiJoin builds the
 *    inner hash table exactly once.
 *
 * Both paths collect execution statistics so the demo/tests can show that
 * decorrelation removes the per-row subquery executions while producing
 * identical results.
 */

import { Expr, SelectStmt } from './ast';
import { PlanNode } from './planner';

export type Row = Record<string, unknown>;
export type Database = Record<string, Row[]>;

export interface Stats {
  subqueryExecutions: number;
  rowsExamined: number;
  comparisons: number;
  hashBuilds: number;
}

export function newStats(): Stats {
  return { subqueryExecutions: 0, rowsExamined: 0, comparisons: 0, hashBuilds: 0 };
}

type Scope = Array<[string, Row]>;

function lookupColumn(
  table: string | null,
  name: string,
  scopes: Scope,
): unknown {
  if (table !== null) {
    for (const [alias, row] of scopes) {
      if (alias === table) return row[name];
    }
    throw new Error(`unknown table alias: ${table}`);
  }
  for (const [, row] of scopes) {
    if (name in row) return row[name];
  }
  throw new Error(`unknown column: ${name}`);
}

function compare(op: string, l: unknown, r: unknown): boolean {
  switch (op) {
    case '=':
      return l === r;
    case '<>':
    case '!=':
      return l !== r;
    case '<':
      return (l as number) < (r as number);
    case '<=':
      return (l as number) <= (r as number);
    case '>':
      return (l as number) > (r as number);
    case '>=':
      return (l as number) >= (r as number);
    default:
      throw new Error(`unsupported operator: ${op}`);
  }
}

export function evalExpr(
  expr: Expr,
  scopes: Scope,
  db: Database,
  stats: Stats,
): unknown {
  switch (expr.kind) {
    case 'literal':
      return expr.value;
    case 'column':
      return lookupColumn(expr.table, expr.name, scopes);
    case 'binary':
      stats.comparisons++;
      return compare(
        expr.op,
        evalExpr(expr.left, scopes, db, stats),
        evalExpr(expr.right, scopes, db, stats),
      );
    case 'and':
      return expr.items.every((i) => Boolean(evalExpr(i, scopes, db, stats)));
    case 'or':
      return expr.items.some((i) => Boolean(evalExpr(i, scopes, db, stats)));
    case 'exists': {
      stats.subqueryExecutions++;
      const found = runExists(expr.subquery, scopes, db, stats);
      return expr.negated ? !found : found;
    }
    case 'in': {
      stats.subqueryExecutions++;
      const value = evalExpr(expr.expr, scopes, db, stats);
      const found = runIn(value, expr.subquery, scopes, db, stats);
      return expr.negated ? !found : found;
    }
  }
}

function runExists(
  sub: SelectStmt,
  outerScopes: Scope,
  db: Database,
  stats: Stats,
): boolean {
  const inner = sub.from[0];
  for (const row of db[inner.table] ?? []) {
    stats.rowsExamined++;
    const scopes: Scope = [[inner.alias, row], ...outerScopes];
    if (!sub.where || Boolean(evalExpr(sub.where, scopes, db, stats))) {
      return true;
    }
  }
  return false;
}

function runIn(
  value: unknown,
  sub: SelectStmt,
  outerScopes: Scope,
  db: Database,
  stats: Stats,
): boolean {
  const inner = sub.from[0];
  const item = sub.select[0];
  if (!item || item.kind !== 'expr') {
    throw new Error('IN subquery must select one expression');
  }
  for (const row of db[inner.table] ?? []) {
    stats.rowsExamined++;
    const scopes: Scope = [[inner.alias, row], ...outerScopes];
    if (sub.where && !Boolean(evalExpr(sub.where, scopes, db, stats))) {
      continue;
    }
    stats.comparisons++;
    if (evalExpr(item.expr, scopes, db, stats) === value) {
      return true;
    }
  }
  return false;
}

/** Naive correlated execution: subquery runs once per outer row. */
export function executeBaseline(
  stmt: SelectStmt,
  db: Database,
): { rows: Row[]; stats: Stats } {
  const stats = newStats();
  const outer = stmt.from[0];
  const rows: Row[] = [];
  for (const row of db[outer.table] ?? []) {
    stats.rowsExamined++;
    const scopes: Scope = [[outer.alias, row]];
    if (!stmt.where || Boolean(evalExpr(stmt.where, scopes, db, stats))) {
      rows.push(row);
    }
  }
  return { rows, stats };
}

function colName(qualified: string): string {
  const idx = qualified.indexOf('.');
  return idx === -1 ? qualified : qualified.slice(idx + 1);
}

function keyOf(row: Row, cols: string[]): string {
  return cols.map((c) => String(row[colName(c)])).join('');
}

/** Execute the decorrelated physical plan. */
export function executePlan(
  node: PlanNode,
  db: Database,
  stats: Stats = newStats(),
): { rows: Row[]; stats: Stats } {
  const rows = runNode(node, db, stats);
  return { rows, stats };
}

function runNode(node: PlanNode, db: Database, stats: Stats): Row[] {
  switch (node.op) {
    case 'SeqScan': {
      const rows = (db[node.table as string] ?? []).slice();
      stats.rowsExamined += rows.length;
      return rows;
    }
    case 'Filter': {
      const input = runNode(node.children[0], db, stats);
      const preds = node.filters ?? node.residual ?? [];
      const alias = node.alias ?? '';
      return input.filter((row) =>
        preds.every((p) => Boolean(evalExpr(p, [[alias, row]], db, stats))),
      );
    }
    case 'HashSemiJoin':
    case 'HashAntiJoin': {
      const probeRows = runNode(node.children[0], db, stats);
      const buildRows = runNode(node.children[1], db, stats);
      stats.hashBuilds++;
      const keys = node.keys ?? [];
      const innerCols = keys.map((k) => k.inner);
      const outerCols = keys.map((k) => k.outer);
      const table = new Set(buildRows.map((r) => keyOf(r, innerCols)));
      const semi = node.op === 'HashSemiJoin';
      return probeRows.filter((row) => {
        stats.comparisons++;
        const hit = table.has(keyOf(row, outerCols));
        return semi ? hit : !hit;
      });
    }
    case 'NestedLoopSemiJoin':
    case 'NestedLoopAntiJoin': {
      const probeRows = runNode(node.children[0], db, stats);
      const buildRows = runNode(node.children[1], db, stats);
      const keys = node.keys ?? [];
      const semi = node.op === 'NestedLoopSemiJoin';
      return probeRows.filter((probe) => {
        const hit = buildRows.some((build) => {
          stats.comparisons++;
          return keys.every(
            (k) => probe[colName(k.outer)] === build[colName(k.inner)],
          );
        });
        return semi ? hit : !hit;
      });
    }
    case 'Project':
      // Semi/anti joins emit outer rows only; projection is a pass-through
      // for the row identity in this engine.
      return runNode(node.children[0], db, stats);
  }
}
