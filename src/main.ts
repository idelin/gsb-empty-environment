// 入口:对每个示例程序做符号执行,输出触发指定断言失败的最小输入,
// 并与全输入穷举结果逐一对照(失败集合 / 溢出 / 循环截断 / 分支可达性)。

import { C, or, type Expr } from "./lang.ts";
import { evalModel } from "./symbolic.ts";
import { runSymbolic } from "./executor.ts";
import { runExhaustive } from "./exhaustive.ts";
import { crossCheck } from "./crosscheck.ts";
import { programs } from "./examples.ts";

const fmtInput = (vec: number[] | null): string =>
  vec === null ? "无(断言不可触发)" : `[${vec.map((v, i) => `in${i}=${v}`).join(", ")}]`;

const disj = (conds: Expr[]): Expr => conds.reduce<Expr>((a, c) => or(a, c), C(0));

let allOk = true;

for (const prog of programs) {
  const sym = runSymbolic(prog);
  const exh = runExhaustive(prog);
  const report = crossCheck(prog, sym, exh);

  console.log(`\n━━━ ${prog.name}(${prog.inputs} 个输入,目标断言 assert#${prog.targetAssert})━━━`);

  const target = report.asserts.find((a) => a.id === prog.targetAssert);
  if (target) {
    console.log(`  最小失败输入(符号执行): ${fmtInput(target.symMin)}`);
    console.log(`  最小失败输入(全输入穷举): ${fmtInput(target.concMin)}  ${target.minMatch ? "✓ 一致" : "✗ 不一致"}`);
    if (target.symMin) {
      const ovfOnPath = evalModel(disj(sym.overflowConds), target.symMin) !== 0;
      console.log(`  最小失败输入路径上发生溢出: ${ovfOnPath ? "是" : "否"}`);
    }
  }
  for (const a of report.asserts) {
    if (a.id !== prog.targetAssert) {
      console.log(`  assert#${a.id}: 最小失败输入 ${fmtInput(a.symMin)}`);
    }
  }

  console.log(`  不可达分支: ${report.unreachable.length === 0 ? "无" : report.unreachable.join(", ")}`);
  console.log(`  溢出输入数: ${exh.overflowSet.size} / ${exh.runs},循环超界截断输入数: ${exh.boundSet.size}`);
  console.log(
    `  状态合并: 合并 ${sym.stats.merged} 个状态,峰值并行状态 ${sym.stats.peakStates},` +
    `求解调用 ${sym.solver.calls} 次(缓存命中 ${sym.solver.cacheHits} 次)`,
  );

  const checks: [string, boolean][] = [
    [`断言失败输入集合一致(${exh.runs} 个输入逐一比对)`, report.asserts.every((a) => a.setMismatches === 0)],
    ["最小失败输入一致", report.asserts.every((a) => a.minMatch)],
    ["溢出输入集合一致", report.overflowMismatches === 0],
    ["循环超界输入集合一致", report.boundMismatches === 0],
    ["分支可达性一致", report.edgeMismatches.length === 0],
  ];
  for (const [label, ok] of checks) console.log(`    ${ok ? "✓" : "✗"} ${label}`);
  if (!report.ok) {
    allOk = false;
    console.log(`  ✗ 对照失败: ${JSON.stringify(report.edgeMismatches)}`);
  } else {
    console.log("  全部对照通过");
  }
}

console.log(allOk ? "\n所有程序:符号执行与全输入穷举完全一致。" : "\n存在不一致,见上。");
process.exitCode = allOk ? 0 : 1;
