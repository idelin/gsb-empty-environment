// 全输入穷举对照：
// 对全部 256^n 个输入，分别用“符号公式求值”和“具体解释执行”得到
// 失败输入集合、最小触发输入、分支可达性、溢出点、循环越界集合，
// 并逐项比对。

import type { Program } from "./ast.ts";
import { execute } from "./executor.ts";
import type { ExecReport } from "./executor.ts";
import { runConcrete } from "./interp.ts";
import { collectSat, enumerateEnvs, envKey, findMin, isSat } from "./solve.ts";
import type { Env } from "./sym.ts";

export interface AssertCheck {
  id: number;
  msg: string;
  symCount: number;
  conCount: number;
  equal: boolean;
}
export interface BranchCheck {
  key: string;
  desc: string;
  reachableSym: boolean;
  reachableCon: boolean;
  equal: boolean;
}
export interface OverflowCheck {
  exprId: number;
  op: string;
  possibleSym: boolean;
  seenCon: boolean;
  equal: boolean;
}

export interface CheckReport {
  exec: ExecReport;
  totalEnvs: number;
  minSym: Env | null;
  minCon: Env | null;
  minEqual: boolean;
  failSymCount: number;
  failConCount: number;
  failSetEqual: boolean;
  assertChecks: AssertCheck[];
  branchChecks: BranchCheck[];
  overflowChecks: OverflowCheck[];
  boundSymCount: number;
  boundConCount: number;
  boundEqual: boolean;
  ok: boolean;
}

function setEqual(a: Set<string>, b: Set<string>): boolean {
  if (a.size !== b.size) return false;
  for (const x of a) if (!b.has(x)) return false;
  return true;
}

export function crossCheck(prog: Program): CheckReport {
  const exec = execute(prog);
  const inputs = prog.inputs;

  // 符号侧：公式枚举求值
  const failSym = collectSat(exec.badFormula, inputs);
  const boundSym = collectSat(exec.boundExceeded, inputs);
  const assertSym = new Map<number, Set<string>>();
  for (const a of exec.asserts) assertSym.set(a.id, collectSat(a.formula, inputs));
  const minSym = findMin(exec.badFormula, inputs);

  // 具体侧：逐输入解释执行
  const failCon = new Set<string>();
  const boundCon = new Set<string>();
  const assertCon = new Map<number, Set<string>>();
  const branchCon = new Set<string>();
  const ovfCon = new Set<number>();
  let minCon: Env | null = null;
  let totalEnvs = 0;
  for (const env of enumerateEnvs(inputs)) {
    totalEnvs++;
    const r = runConcrete(prog, env);
    const key = envKey(inputs, env);
    if (r.failedAsserts.length > 0) {
      failCon.add(key);
      if (minCon === null) minCon = env; // 枚举顺序即字典序，首个即最小
    }
    for (const id of r.failedAsserts) {
      let set = assertCon.get(id);
      if (!set) {
        set = new Set();
        assertCon.set(id, set);
      }
      set.add(key);
    }
    if (r.boundExceeded) boundCon.add(key);
    for (const b of r.branches) branchCon.add(b);
    for (const o of r.overflowSites) ovfCon.add(o);
  }

  const minEqual =
    (minSym === null && minCon === null) ||
    (minSym !== null && minCon !== null && envKey(inputs, minSym) === envKey(inputs, minCon));

  const assertChecks: AssertCheck[] = exec.asserts.map((a) => {
    const symSet = assertSym.get(a.id) ?? new Set<string>();
    const conSet = assertCon.get(a.id) ?? new Set<string>();
    return {
      id: a.id,
      msg: a.msg,
      symCount: symSet.size,
      conCount: conSet.size,
      equal: setEqual(symSet, conSet),
    };
  });

  const branchChecks: BranchCheck[] = exec.branches.map((b) => {
    const reachableSym = isSat(b.formula, inputs);
    const reachableCon = branchCon.has(b.key);
    return { key: b.key, desc: b.desc, reachableSym, reachableCon, equal: reachableSym === reachableCon };
  });

  const overflowChecks: OverflowCheck[] = exec.overflows.map((o) => {
    const possibleSym = isSat(o.formula, inputs);
    const seenCon = ovfCon.has(o.exprId);
    return { exprId: o.exprId, op: o.op, possibleSym, seenCon, equal: possibleSym === seenCon };
  });

  const failSetEqual = setEqual(failSym, failCon);
  const boundEqual = setEqual(boundSym, boundCon);
  const ok =
    failSetEqual &&
    minEqual &&
    boundEqual &&
    assertChecks.every((c) => c.equal) &&
    branchChecks.every((c) => c.equal) &&
    overflowChecks.every((c) => c.equal);

  return {
    exec,
    totalEnvs,
    minSym,
    minCon,
    minEqual,
    failSymCount: failSym.size,
    failConCount: failCon.size,
    failSetEqual,
    assertChecks,
    branchChecks,
    overflowChecks,
    boundSymCount: boundSym.size,
    boundConCount: boundCon.size,
    boundEqual,
    ok,
  };
}
