// 测试入口：
//   1. 三值逻辑真值表
//   2. 空值比较 => UNKNOWN；筛选只保留 TRUE
//   3. 外连接补空与真实空值行为一致
//   4. 小表穷举：合法下推规则前后等价
//   5. 小表穷举：优化器（pushDown）前后等价
//   6. 非法下推反例验证（必须不等价）
import type { Tri } from '../src/trivalent.js';
import { triAnd, triOr, triNot } from '../src/trivalent.js';
import type { Expr, Row, Value } from '../src/expr.js';
import { col, lit, cmp, and, evalExpr, formatExpr } from '../src/expr.js';
import { filter, project, join } from '../src/operators.js';
import type { JoinType, Table } from '../src/operators.js';
import type { Plan } from '../src/plan.js';
import { scan, filterNode, joinNode, projectNode, execute, formatPlan } from '../src/plan.js';
import { pushDown } from '../src/pushdown.js';
import { checkEquivalence, sameMultiset } from '../src/equiv.js';
import { verifyCounterexamples } from '../src/counterexamples.js';

let failures = 0;
let passes = 0;
function assert(cond: boolean, msg: string): void {
  if (cond) {
    passes++;
  } else {
    failures++;
    console.error(`FAIL: ${msg}`);
  }
}

// ---------- 1. 三值逻辑真值表 ----------
const tris: Tri[] = ['T', 'F', 'U'];
const AND_TABLE: Record<string, Tri> = {
  'T,T': 'T', 'T,F': 'F', 'T,U': 'U',
  'F,T': 'F', 'F,F': 'F', 'F,U': 'F',
  'U,T': 'U', 'U,F': 'F', 'U,U': 'U',
};
const OR_TABLE: Record<string, Tri> = {
  'T,T': 'T', 'T,F': 'T', 'T,U': 'T',
  'F,T': 'T', 'F,F': 'F', 'F,U': 'U',
  'U,T': 'T', 'U,F': 'U', 'U,U': 'U',
};
for (const a of tris) {
  for (const b of tris) {
    assert(triAnd(a, b) === AND_TABLE[`${a},${b}`], `AND ${a} ${b}`);
    assert(triOr(a, b) === OR_TABLE[`${a},${b}`], `OR ${a} ${b}`);
  }
}
assert(triNot('T') === 'F' && triNot('F') === 'T' && triNot('U') === 'U', 'NOT table');

// ---------- 2. 空值比较与筛选语义 ----------
assert(evalExpr(cmp('=', lit(null), lit(null)), {}) === 'U', 'NULL = NULL is UNKNOWN');
assert(evalExpr(cmp('=', col('a'), lit(1)), { a: null }) === 'U', 'NULL = 1 is UNKNOWN');
assert(evalExpr(cmp('>', col('a'), lit(0)), { a: null }) === 'U', 'NULL > 0 is UNKNOWN');
assert(evalExpr(cmp('<>', col('a'), lit(0)), { a: null }) === 'U', 'NULL <> 0 is UNKNOWN');

const rows2: Row[] = [{ a: 1 }, { a: -1 }, { a: null }];
const kept = filter(rows2, cmp('>', col('a'), lit(0)));
assert(kept.length === 1 && kept[0].a === 1, 'filter keeps only TRUE (drops FALSE and UNKNOWN)');

// ---------- 3. 外连接补空与真实空值遵循同一规则 ----------
const L3: Table = { name: 'L', rows: [{ k: 1, a: 10 }, { k: 2, a: 20 }], schemaCols: ['k', 'a'] };
const R3: Table = { name: 'R', rows: [{ k: 1, y: null }, { k: 3, y: 5 }], schemaCols: ['k', 'y'] };
const joined3 = join(L3, R3, ['k'], ['k'], 'left');
// k=1 匹配到真实 NULL 的 y；k=2 未匹配补 NULL。两者 y 都是 NULL，过滤行为必须一致。
const filtered3 = filter(joined3, cmp('>', col('y'), lit(0)));
assert(filtered3.length === 0, 'real NULL and padded NULL both yield UNKNOWN and are dropped');
const keptAll3 = filter(joined3, cmp('=', lit(1), lit(1)));
assert(keptAll3.length === 2, 'outer join keeps unmatched left rows (padded)');

// 投影
const proj = project([{ a: 1, b: 2 }], ['a']);
assert(proj.length === 1 && proj[0].a === 1 && !('b' in proj[0]), 'project keeps selected cols');

// ---------- 4/5. 小表穷举等价性 ----------
const domain: Value[] = [null, 0, 1, 2];
const leftCols = ["k", "a"];
const rightCols = ["k", "y"];
const maxRows = 2;

function mkScan(name: string, rows: Row[], cols: string[]): Plan {
  return scan({ name, rows, schemaCols: cols });
}
function mkJoin(jt: JoinType, l: Row[], r: Row[]): Plan {
  return joinNode(jt, ['k'], ['k'], mkScan('L', l, leftCols), mkScan('R', r, rightCols));
}

function runEquiv(name: string, jt: JoinType, pred: Expr): void {
  const res = checkEquivalence({
    leftCols,
    rightCols,
    domain,
    maxRows,
    makeBefore: (l, r) => filterNode(pred, mkJoin(jt, l, r)),
    makeAfter: (l, r) => pushDown(filterNode(pred, mkJoin(jt, l, r))),
  });
  assert(res.equivalent, `pushdown equivalence [${name}] join=${jt} pred=${formatExpr(pred)}` +
    (res.counterexample ? ` cx=${JSON.stringify(res.counterexample)}` : ''));
  console.log(`ok  [${name}] ${jt.padEnd(5)} pred=${formatExpr(pred)}  (${res.checkedCases} cases)`);
}

const pLeft = cmp('>', col('a'), lit(0));
const pLeft2 = cmp('=', col('a'), lit(1));
const pRight = cmp('<', col('y'), lit(2));
const pCross = cmp('<', col('a'), col('y'));

console.log('--- exhaustive equivalence: optimizer pushDown ---');
for (const jt of ['inner', 'left', 'right', 'full'] as JoinType[]) {
  runEquiv('left-only', jt, pLeft);
  runEquiv('right-only', jt, pRight);
  runEquiv('both-sides', jt, and(pLeft, pRight));
  runEquiv('with-cross', jt, and(pLeft, pCross));
  runEquiv('all-three', jt, and(pLeft, pRight, pCross));
  runEquiv('left-eq', jt, pLeft2);
}

// ---------- 6. 非法下推反例 ----------
console.log('--- illegal pushdown counterexamples ---');
for (const { cx, differs } of verifyCounterexamples()) {
  assert(differs, `counterexample must differ: ${cx.name}`);
  console.log(`ok  ${cx.name}`);
  console.log(`    正确结果: ${JSON.stringify(cx.before)}`);
  console.log(`    非法下推: ${JSON.stringify(cx.after)}`);
}

// 展示一个下推前后的计划形状
const demo = filterNode(and(pLeft, pRight), mkJoin('left', [{ k: 1, a: 1 }], [{ k: 1, y: 1 }]));
console.log('--- plan demo (left join) ---');
console.log('before:\n' + formatPlan(demo));
console.log('after:\n' + formatPlan(pushDown(demo)));

console.log(`\n${passes} passed, ${failures} failed`);
if (failures > 0) process.exit(1);
