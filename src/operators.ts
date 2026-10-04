// 关系算子：筛选（只保留 TRUE）、投影、四种连接。
// 外连接补出的 NULL 与数据中的真实 NULL 走同一套求值规则（见 expr.ts）。
import type { Expr, Row, Value } from './expr.js';
import { evalExpr } from './expr.js';

export type JoinType = 'inner' | 'left' | 'right' | 'full';

export interface Table {
  name: string;
  rows: Row[];
  schemaCols?: string[];
}

// 筛选：三值结果中只有 TRUE 的行被保留（FALSE 与 UNKNOWN 都丢弃）。
export function filter(rows: Row[], pred: Expr): Row[] {
  return rows.filter((r) => evalExpr(pred, r) === 'T');
}

// 投影：仅保留指定列。
export function project(rows: Row[], cols: string[]): Row[] {
  return rows.map((r) => {
    const out: Row = {};
    for (const c of cols) out[c] = r[c] === undefined ? null : r[c];
    return out;
  });
}

function mergeRow(a: Row, b: Row): Row {
  return { ...a, ...b };
}

// 补空：只补上 base 中不存在的列，避免覆盖保留侧的真实值（如连接键）。
// 注意：本引擎采用扁平列命名空间，左右表的非键列名应互不相同。
function padRow(base: Row, padCols: string[]): Row {
  const out: Row = { ...base };
  for (const c of padCols) if (!(c in out)) out[c] = null;
  return out;
}

const NULL_KEY = '@@NULL@@';

function rowKey(r: Row, cols: string[]): string {
  // NULL 与 NULL 在连接匹配中不相等（SQL 语义），用哨兵保证它们永不匹配。
  return cols
    .map((c) => {
      const v: Value = r[c] === undefined ? null : r[c];
      return v === null ? NULL_KEY : `${typeof v}:${String(v)}`;
    })
    .join('|');
}

// 等值连接（NULL 键互不匹配），外连接侧未匹配的行以 NULL 补齐。
export function join(
  left: Table,
  right: Table,
  leftKeys: string[],
  rightKeys: string[],
  type: JoinType,
): Row[] {
  const rightCols = right.rows.length > 0 ? Object.keys(right.rows[0]) : right.schemaCols ?? [];
  const leftCols = left.rows.length > 0 ? Object.keys(left.rows[0]) : left.schemaCols ?? [];

  const index = new Map<string, Row[]>();
  for (const r of right.rows) {
    const k = rowKey(r, rightKeys);
    if (k.includes(NULL_KEY)) continue; // NULL 键不参与匹配
    const arr = index.get(k);
    if (arr) arr.push(r);
    else index.set(k, [r]);
  }

  const matchedRight = new Set<Row>();
  const out: Row[] = [];

  for (const l of left.rows) {
    const k = rowKey(l, leftKeys);
    const matches = k.includes(NULL_KEY) ? undefined : index.get(k);
    if (matches && matches.length > 0) {
      for (const m of matches) {
        matchedRight.add(m);
        out.push(mergeRow(l, m));
      }
    } else if (type === 'left' || type === 'full') {
      out.push(padRow(l, rightCols));
    }
  }

  if (type === 'right' || type === 'full') {
    for (const r of right.rows) {
      if (!matchedRight.has(r)) out.push(padRow(r, leftCols));
    }
  }
  return out;
}
