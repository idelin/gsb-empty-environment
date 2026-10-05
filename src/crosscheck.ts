// 对照检查:把符号执行得到的条件在每个输入上求值,与穷举的具体执行逐一比对。
// 比对内容:各断言的失败输入集合、最小失败输入、溢出输入集合、
// 循环超界输入集合、每条分支边的可达性。

import { C, or, collectEdges, collectAssertIds, type Expr, type Program } from "./lang.ts";
import { evalModel } from "./symbolic.ts";
import type { SymAnalysis } from "./executor.ts";
import { forEachInput, inputKey, type ExhaustiveResult } from "./exhaustive.ts";

const disj = (conds: Expr[]): Expr => conds.reduce<Expr>((a, c) => or(a, c), C(0));

export type AssertReport = {
  id: number;
  symMin: number[] | null;
  concMin: number[] | null;
  minMatch: boolean;
  setMismatches: number;
};

export type Report = {
  asserts: AssertReport[];
  overflowMismatches: number;
  boundMismatches: number;
  unreachable: string[];      // 符号执行判定不可达的分支(与穷举一致)
  edgeMismatches: string[];   // 可达性结论不一致的分支
  ok: boolean;
};

export function crossCheck(prog: Program, sym: SymAnalysis, exh: ExhaustiveResult): Report {
  const solver = sym.solver;
  const failConds = new Map<number, Expr>();
  const asserts: AssertReport[] = [];

  for (const id of collectAssertIds(prog)) {
    const F = disj(sym.failures.get(id) ?? []);
    failConds.set(id, F);
    const symMin = solver.solveMin(F);
    const concMin = exh.minFailing.get(id) ?? null;
    asserts.push({
      id,
      symMin,
      concMin,
      minMatch: JSON.stringify(symMin) === JSON.stringify(concMin),
      setMismatches: 0,
    });
  }

  const O = disj(sym.overflowConds);
  const B = disj(sym.boundExceeded);
  let overflowMismatches = 0;
  let boundMismatches = 0;

  forEachInput(prog.inputs, (vec) => {
    const key = inputKey(vec);
    for (const a of asserts) {
      const symFail = evalModel(failConds.get(a.id)!, vec) !== 0;
      const concFail = exh.failSet.get(a.id)?.has(key) ?? false;
      if (symFail !== concFail) a.setMismatches++;
    }
    if ((evalModel(O, vec) !== 0) !== exh.overflowSet.has(key)) overflowMismatches++;
    if ((evalModel(B, vec) !== 0) !== exh.boundSet.has(key)) boundMismatches++;
  });

  const unreachable: string[] = [];
  const edgeMismatches: string[] = [];
  for (const edge of collectEdges(prog)) {
    const conds = sym.edgeConds.get(edge);
    const symReachable = conds !== undefined && solver.satisfiable(disj(conds));
    const concReachable = exh.edges.has(edge);
    if (!symReachable) unreachable.push(edge);
    if (symReachable !== concReachable) edgeMismatches.push(edge);
  }

  const ok =
    asserts.every((a) => a.minMatch && a.setMismatches === 0) &&
    overflowMismatches === 0 &&
    boundMismatches === 0 &&
    edgeMismatches.length === 0;

  return { asserts, overflowMismatches, boundMismatches, unreachable, edgeMismatches, ok };
}
