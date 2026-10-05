/**
 * 谓词下推等价性的穷举检查：
 * 在小值域 {0, 1, NULL} 上枚举所有不超过 maxRows 行的小表，
 * 对“下推后的计划”和“原计划”逐例比较输出（包语义，按多重集合比较）。
 */

import {
  Expr, Table, Value, Row,
  col, lit, cmp, or, not, isnull,
  filter, project, join, evalPred,
} from './engine';

export const DOMAIN: Value[] = [0, 1, null];

/** 生成单列 colName 上所有 0..maxRows 行的表。 */
export function allTables(colName: string, maxRows: number): Table[] {
  const tuples: Row[] = DOMAIN.map((v) => ({ [colName]: v }));
  const tables: Table[] = [];
  const gen = (n: number, acc: Row[]): void => {
    if (n === 0) {
      tables.push({ cols: [colName], rows: [...acc] });
      return;
    }
    for (const t of tuples) {
      acc.push(t);
      gen(n - 1, acc);
      acc.pop();
    }
  };
  for (let n = 0; n <= maxRows; n++) gen(n, []);
  return tables;
}

/** 规范化：行内按列序、行间排序，得到多重集合的规范字符串。 */
export function canon(t: Table): string {
  const norm = t.rows.map((r) =>
    JSON.stringify(t.cols.map((c) => [c, r[c] === undefined ? null : r[c]])),
  );
  norm.sort();
  return norm.join('|');
}

export interface Pred {
  name: string;
  e: Expr;
}

/** 针对某一列的谓词库（含可空、含三值逻辑边界情形）。 */
export function predLib(c: string): Pred[] {
  return [
    { name: `${c} = 0`, e: cmp('=', col(c), lit(0)) },
    { name: `${c} = 1`, e: cmp('=', col(c), lit(1)) },
    { name: `${c} <> 0`, e: cmp('<>', col(c), lit(0)) },
    { name: `${c} < 1`, e: cmp('<', col(c), lit(1)) },
    { name: `${c} IS NULL`, e: isnull(col(c)) },
    { name: `NOT (${c} IS NULL)`, e: not(isnull(col(c))) },
    { name: `NOT (${c} = 0)`, e: not(cmp('=', col(c), lit(0))) },
    { name: `${c} = 0 OR ${c} = 1`, e: or(cmp('=', col(c), lit(0)), cmp('=', col(c), lit(1))) },
    { name: `${c} = ${c}`, e: cmp('=', col(c), col(c)) },
    { name: 'TRUE', e: lit(true) },
  ];
}

/** 拒空谓词：该列取 NULL 时谓词不为真（筛选会丢弃该行）。 */
export function isNullRejecting(p: Expr, c: string): boolean {
  const row: Row = {};
  row[c] = null;
  return evalPred(p, row) !== 'T';
}

export interface Cex {
  pred: string;
  inputs: Table[];
  pushedOut: Table;
  origOut: Table;
}

export interface RuleResult {
  rule: string;
  cases: number;
  cexCount: number;
  samples: Cex[];
}

export interface BinRule {
  name: string;
  side: 'a' | 'b';
  preds?: Pred[];
  pushed: (a: Table, b: Table, p: Expr) => Table;
  original: (a: Table, b: Table, p: Expr) => Table;
}

export function checkBinRule(rule: BinRule, maxRows = 2): RuleResult {
  const tas = allTables('a', maxRows);
  const tbs = allTables('b', maxRows);
  const preds = rule.preds ?? predLib(rule.side);
  let cases = 0;
  let cexCount = 0;
  const samples: Cex[] = [];
  for (const p of preds) {
    for (const a of tas) {
      for (const b of tbs) {
        cases++;
        const pushedOut = rule.pushed(a, b, p.e);
        const origOut = rule.original(a, b, p.e);
        if (canon(pushedOut) !== canon(origOut)) {
          cexCount++;
          if (samples.length < 3) {
            samples.push({ pred: p.name, inputs: [a, b], pushedOut, origOut });
          }
        }
      }
    }
  }
  return { rule: rule.name, cases, cexCount, samples };
}

export function checkUnaryRule(
  name: string,
  side: 'a' | 'b',
  pushed: (t: Table, p: Expr) => Table,
  original: (t: Table, p: Expr) => Table,
  maxRows = 2,
): RuleResult {
  const tables = allTables(side, maxRows);
  const preds = predLib(side);
  let cases = 0;
  let cexCount = 0;
  const samples: Cex[] = [];
  for (const p of preds) {
    for (const t of tables) {
      cases++;
      const pushedOut = pushed(t, p.e);
      const origOut = original(t, p.e);
      if (canon(pushedOut) !== canon(origOut)) {
        cexCount++;
        if (samples.length < 3) {
          samples.push({ pred: p.name, inputs: [t], pushedOut, origOut });
        }
      }
    }
  }
  return { rule: name, cases, cexCount, samples };
}

const TRUE = lit(true);
const A_EQ_B = cmp('=', col('a'), col('b'));

/** 连接谓词下推规则集：pushed 为下推后的计划，original 为原计划。 */
export const BIN_RULES: BinRule[] = [
  {
    name: 'R1  σp(A ⋈ B) ≡ σp(A) ⋈ B   [内连接·笛卡尔积, p 只引用 a]',
    side: 'a',
    pushed: (a, b, p) => join(filter(a, p), b, TRUE, 'inner'),
    original: (a, b, p) => filter(join(a, b, TRUE, 'inner'), p),
  },
  {
    name: 'R2  σp(A ⋈ B) ≡ σp(A) ⋈ B   [内连接·等值 a=b, p 只引用 a]',
    side: 'a',
    pushed: (a, b, p) => join(filter(a, p), b, A_EQ_B, 'inner'),
    original: (a, b, p) => filter(join(a, b, A_EQ_B, 'inner'), p),
  },
  {
    name: 'R3  σp(A ⋈ B) ≡ A ⋈ σp(B)   [内连接·等值 a=b, p 只引用 b]',
    side: 'b',
    pushed: (a, b, p) => join(a, filter(b, p), A_EQ_B, 'inner'),
    original: (a, b, p) => filter(join(a, b, A_EQ_B, 'inner'), p),
  },
  {
    name: 'R4  σp(A ⟕ B) ≡ σp(A) ⟕ B   [左外连接, p 只引用保留侧 a]',
    side: 'a',
    pushed: (a, b, p) => join(filter(a, p), b, A_EQ_B, 'left'),
    original: (a, b, p) => filter(join(a, b, A_EQ_B, 'left'), p),
  },
  {
    name: 'R5  σp(A ⟕ B) ≡ A ⟕ σp(B)   [左外连接, p 只引用补空侧 b] —— 一般不成立',
    side: 'b',
    pushed: (a, b, p) => join(a, filter(b, p), A_EQ_B, 'left'),
    original: (a, b, p) => filter(join(a, b, A_EQ_B, 'left'), p),
  },
  {
    name: 'R6  σp(A ⟗ B) ≡ σp(A) ⟗ B   [全外连接, p 只引用 a] —— 一般不成立',
    side: 'a',
    pushed: (a, b, p) => join(filter(a, p), b, A_EQ_B, 'full'),
    original: (a, b, p) => filter(join(a, b, A_EQ_B, 'full'), p),
  },
  {
    name: 'R7  σp(A ⟕ B) ≡ A ⋈ σp(B)   [左外连接退化为内连接, 仅拒空谓词]',
    side: 'b',
    preds: predLib('b').filter((pr) => isNullRejecting(pr.e, 'b')),
    pushed: (a, b, p) => join(a, filter(b, p), A_EQ_B, 'inner'),
    original: (a, b, p) => filter(join(a, b, A_EQ_B, 'left'), p),
  },
];

/** 投影与筛选交换（谓词引用的列均被保留时）。 */
export function checkProjectFilter(maxRows = 2): RuleResult {
  return checkUnaryRule(
    'R8  π{a}(σp A) ≡ σp(π{a} A)   [p 只引用被保留的列 a]',
    'a',
    (t, p) => project(filter(t, p), ['a']),
    (t, p) => filter(project(t, ['a']), p),
    maxRows,
  );
}
