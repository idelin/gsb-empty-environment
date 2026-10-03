/**
 * Query plan model + a tiny parser for the supported SQL subset:
 *
 *   SELECT * FROM <table> WHERE <col> <op> <num> ORDER BY <col> [ASC|DESC] LIMIT <n>
 *
 * Every clause except SELECT/FROM is optional.
 */

export type CompareOp = '>' | '>=' | '<' | '<=' | '=' | '!=';

export interface FilterClause {
  column: string;
  op: CompareOp;
  value: number;
}

export interface OrderByClause {
  column: string;
  direction: 'asc' | 'desc';
}

export interface QueryPlan {
  filter?: FilterClause;
  orderBy?: OrderByClause;
  limit?: number;
}

const QUERY_RE =
  /^\s*SELECT\s+\*\s+FROM\s+(\w+)(?:\s+WHERE\s+(\w+)\s*(>=|<=|!=|=|>|<)\s*(-?\d+(?:\.\d+)?))?(?:\s+ORDER\s+BY\s+(\w+)(?:\s+(ASC|DESC))?)?(?:\s+LIMIT\s+(\d+))?\s*;?\s*$/i;

export function parseQuery(sql: string): QueryPlan {
  const m = QUERY_RE.exec(sql);
  if (!m) {
    throw new Error(`unsupported query: ${JSON.stringify(sql)}`);
  }
  const plan: QueryPlan = {};
  if (m[2] !== undefined) {
    plan.filter = { column: m[2], op: m[3] as CompareOp, value: Number(m[4]) };
  }
  if (m[5] !== undefined) {
    plan.orderBy = {
      column: m[5],
      direction: m[6]?.toLowerCase() === 'desc' ? 'desc' : 'asc',
    };
  }
  if (m[7] !== undefined) {
    plan.limit = Number(m[7]);
  }
  return plan;
}

export function compare(a: number, op: CompareOp, b: number): boolean {
  switch (op) {
    case '>': return a > b;
    case '>=': return a >= b;
    case '<': return a < b;
    case '<=': return a <= b;
    case '=': return a === b;
    case '!=': return a !== b;
  }
}
