import {
  Table, Value, Tri,
  col, lit, cmp, isnull,
  evalPred, filter, join,
  triAnd, triOr, triNot,
} from './engine';
import { BIN_RULES, checkBinRule, checkProjectFilter, RuleResult } from './pushdown';

// ---------------- 打印工具 ----------------

function fmtValue(v: Value | undefined): string {
  if (v === undefined || v === null) return 'NULL';
  return JSON.stringify(v);
}

function fmtTable(t: Table): string {
  if (t.rows.length === 0) return '∅';
  return t.rows
    .map((r) => '{ ' + t.cols.map((c) => `${c}=${fmtValue(r[c])}`).join(', ') + ' }')
    .join('\n    ');
}

function fmtTri(t: Tri): string {
  return t === 'T' ? 'TRUE' : t === 'F' ? 'FALSE' : 'UNKNOWN';
}

// ---------------- 演示 1：三值逻辑 ----------------

function demoThreeValuedLogic(): void {
  console.log('== 1. 三值逻辑（真/假/未知） ==');
  const ts: Tri[] = ['T', 'F', 'U'];
  const name = (t: Tri) => fmtTri(t);
  console.log('NOT: ' + ts.map((t) => `¬${name(t)}=${name(triNot(t))}`).join('  '));
  console.log('AND:');
  for (const a of ts) {
    console.log('  ' + ts.map((b) => `${name(a)}∧${name(b)}=${name(triAnd(a, b))}`).join('  '));
  }
  console.log('OR:');
  for (const a of ts) {
    console.log('  ' + ts.map((b) => `${name(a)}∨${name(b)}=${name(triOr(a, b))}`).join('  '));
  }
  const row = { x: null as Value, y: 1 as Value };
  console.log('空值比较:');
  console.log(`  NULL = NULL  -> ${fmtTri(evalPred(cmp('=', col('x'), col('x')), row))}`);
  console.log(`  NULL = 1     -> ${fmtTri(evalPred(cmp('=', col('x'), col('y')), row))}`);
  console.log(`  NULL IS NULL -> ${fmtTri(evalPred(isnull(col('x')), row))}`);
  console.log('');
}

// ---------------- 演示 2：补出的 NULL 与真实 NULL 同规则 ----------------

function demoPaddedNulls(): void {
  console.log('== 2. 外连接补出的 NULL 与真实 NULL 遵循同一规则 ==');
  const A: Table = {
    cols: ['k1', 'x'],
    rows: [
      { k1: 1, x: 'a' },
      { k1: 2, x: 'b' },
    ],
  };
  const B: Table = {
    cols: ['k2', 'y'],
    rows: [{ k2: 1, y: null }], // 真实 NULL
  };
  const J = join(A, B, cmp('=', col('k1'), col('k2')), 'left');
  console.log('A ⟕ B (A.k1 = B.k2):');
  console.log('    ' + fmtTable(J));
  console.log('  第 1 行 y 是数据中的真实 NULL，第 2 行 y 是外连接补出的 NULL。');

  const probes: { label: string; p: ReturnType<typeof cmp> | ReturnType<typeof isnull> }[] = [
    { label: 'y = 0', p: cmp('=', col('y'), lit(0)) },
    { label: 'y = y', p: cmp('=', col('y'), col('y')) },
    { label: 'y IS NULL', p: isnull(col('y')) },
  ];
  for (const { label, p } of probes) {
    console.log(`  σ[${label}](A ⟕ B) = ${fmtTable(filter(J, p))}`);
  }
  console.log('  两种 NULL 在筛选下行为完全一致（比较得 UNKNOWN 被丢弃，IS NULL 为真被保留）。');
  console.log('');
}

// ---------------- 演示 3：穷举检查谓词下推 ----------------

function printResult(r: RuleResult): void {
  const ok = r.cexCount === 0;
  console.log(`${ok ? '✓ 成立  ' : '✗ 不成立'}  ${r.rule}`);
  console.log(`         检查 ${r.cases} 个用例，反例 ${r.cexCount} 个`);
  for (const s of r.samples.slice(0, 2)) {
    console.log(`         反例: p = ${s.pred}`);
    s.inputs.forEach((t, i) => {
      console.log(`           ${String.fromCharCode(65 + i)} = ${fmtTable(t)}`);
    });
    console.log(`           原计划   = ${fmtTable(s.origOut)}`);
    console.log(`           下推后   = ${fmtTable(s.pushedOut)}`);
  }
  console.log('');
}

function main(): void {
  demoThreeValuedLogic();
  demoPaddedNulls();
  console.log('== 3. 小表穷举检查谓词下推（值域 {0,1,NULL}，每表 ≤2 行，包语义） ==');
  console.log('');
  for (const rule of BIN_RULES) {
    printResult(checkBinRule(rule));
  }
  printResult(checkProjectFilter());
}

main();
