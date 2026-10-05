// 演示入口：对每个示例程序做符号执行 + 全输入穷举对照，并打印报告。

import { crossCheck } from "./check.ts";
import type { CheckReport } from "./check.ts";
import { demoProgram, safeProgram, unreachableProgram } from "./examples.ts";
import { fmtEnv } from "./solve.ts";
import { showSym, symSize } from "./sym.ts";
import type { Program } from "./ast.ts";

function printReport(name: string, prog: Program, rep: CheckReport): void {
  console.log("=".repeat(64));
  console.log(`程序 ${name}   输入: [${prog.inputs.join(", ")}]   循环上界: ${prog.loopBound}`);
  console.log(`符号执行: 创建状态 ${rep.exec.statesCreated} 个, 合并 ${rep.exec.statesMerged} 次`);
  for (const a of rep.exec.asserts) {
    const f = showSym(a.formula);
    const text = f.length > 150 ? `${f.slice(0, 150)} …` : f;
    console.log(`断言#${a.id} (${a.msg}) 失败条件: ${text}`);
  }
  console.log(`失败条件公式规模: ${symSize(rep.exec.badFormula)} 个节点`);
  if (rep.minSym) {
    console.log(`>>> 触发断言失败的最小输入: ${fmtEnv(prog.inputs, rep.minSym)}`);
  } else {
    console.log(">>> 不存在触发断言失败的输入");
  }
  const unreach = rep.branchChecks.filter((b) => !b.reachableSym);
  console.log(
    unreach.length === 0
      ? "不可达分支: 无"
      : `不可达分支: ${unreach.map((b) => b.desc).join("; ")}`,
  );
  const ovfs = rep.overflowChecks.filter((o) => o.possibleSym);
  console.log(
    ovfs.length === 0
      ? "溢出检查: 没有可能溢出的运算"
      : `溢出检查: ${ovfs.map((o) => `expr#${o.exprId}(${o.op})`).join(", ")} 可能溢出`,
  );
  console.log(
    rep.boundSymCount === 0
      ? "循环上界: 所有输入都在上界内退出"
      : `循环上界: ${rep.boundSymCount} 个输入超出上界（按未验证路径丢弃）`,
  );
  console.log(`--- 全输入穷举对照（${rep.totalEnvs} 个输入） ---`);
  const mark = (b: boolean): string => (b ? "一致 ✓" : "不一致 ✗");
  console.log(`失败输入集合: 符号 ${rep.failSymCount} 个 vs 穷举 ${rep.failConCount} 个  [${mark(rep.failSetEqual)}]`);
  const minSymText = rep.minSym ? fmtEnv(prog.inputs, rep.minSym) : "无";
  const minConText = rep.minCon ? fmtEnv(prog.inputs, rep.minCon) : "无";
  console.log(`最小触发输入: 符号 ${minSymText} vs 穷举 ${minConText}  [${mark(rep.minEqual)}]`);
  console.log(`逐断言失败集合: ${rep.assertChecks.every((c) => c.equal) ? "全部一致 ✓" : "存在不一致 ✗"}`);
  console.log(`分支可达性:     ${rep.branchChecks.every((c) => c.equal) ? "全部一致 ✓" : "存在不一致 ✗"}`);
  console.log(`溢出点:         ${rep.overflowChecks.every((c) => c.equal) ? "全部一致 ✓" : "存在不一致 ✗"}`);
  console.log(`循环越界集合:   符号 ${rep.boundSymCount} 个 vs 穷举 ${rep.boundConCount} 个  [${mark(rep.boundEqual)}]`);
  console.log(`总体: ${rep.ok ? "PASS" : "FAIL"}`);
}

const programs: Array<[string, Program]> = [
  ["demo", demoProgram()],
  ["unreachable", unreachableProgram()],
  ["safe", safeProgram()],
];

let allOk = true;
for (const [name, prog] of programs) {
  const rep = crossCheck(prog);
  printReport(name, prog, rep);
  allOk = allOk && rep.ok;
}
process.exit(allOk ? 0 : 1);
