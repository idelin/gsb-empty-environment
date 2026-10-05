// 符号执行器。
// 状态 = { pc: 路径条件, store: 符号存储, ovf: “路径上已发生溢出”的条件 }。
// 合并策略:同一程序点(每条语句之后、循环头每次迭代)上,符号存储完全相同
// 的状态被合并为一个,pc 取各路径条件的析取(各自路径条件因此被保留),
// ovf 取 ∨(pc_i ∧ ovf_i)。存储不同的状态保留为同一位置下的多个状态。

import { C, and, or, not, truthy, type Expr, type Program, type Stmt } from "./lang.ts";
import { Solver, evalSym, type SymStore } from "./symbolic.ts";

export type SymState = {
  pc: Expr;
  store: SymStore;
  ovf: Expr;
};

export type SymAnalysis = {
  failures: Map<number, Expr[]>;   // assertId -> 失败路径条件(pc ∧ ¬cond)列表
  edgeConds: Map<string, Expr[]>;  // 分支边 -> 到达该边的条件列表
  overflowConds: Expr[];           // 各终止点的 (pc ∧ ovf)
  boundExceeded: Expr[];           // 循环超界被截断的路径条件
  solver: Solver;
  stats: { merged: number; peakStates: number };
};

export function runSymbolic(prog: Program): SymAnalysis {
  const solver = new Solver(prog.inputs);
  const analysis: SymAnalysis = {
    failures: new Map(),
    edgeConds: new Map(),
    overflowConds: [],
    boundExceeded: [],
    solver,
    stats: { merged: 0, peakStates: 0 },
  };

  const recordEdge = (name: string, cond: Expr): void => {
    const list = analysis.edgeConds.get(name);
    if (list) list.push(cond);
    else analysis.edgeConds.set(name, [cond]);
  };

  const sat = (e: Expr): boolean => solver.satisfiable(e);

  function mergeStates(states: SymState[]): SymState[] {
    const groups = new Map<string, SymState[]>();
    for (const s of states) {
      const key = JSON.stringify([...s.store.entries()].sort((a, b) => (a[0] < b[0] ? -1 : 1)));
      const g = groups.get(key);
      if (g) g.push(s);
      else groups.set(key, [s]);
    }
    const out: SymState[] = [];
    for (const group of groups.values()) {
      if (group.length === 1) {
        out.push(group[0]);
      } else {
        analysis.stats.merged += group.length - 1;
        let pc: Expr = C(0);
        let ovf: Expr = C(0);
        for (const s of group) {
          pc = or(pc, s.pc);                 // 保留各自路径条件(析取)
          ovf = or(ovf, and(s.pc, s.ovf));
        }
        out.push({ pc, store: group[0].store, ovf });
      }
    }
    return out;
  }

  function execBlock(stmts: Stmt[], states: SymState[]): SymState[] {
    for (const stmt of stmts) {
      states = mergeStates(execStmt(stmt, states));
      analysis.stats.peakStates = Math.max(analysis.stats.peakStates, states.length);
      if (states.length === 0) break;
    }
    return states;
  }

  function execStmt(stmt: Stmt, states: SymState[]): SymState[] {
    switch (stmt.kind) {
      case "assign":
        return states.map((s) => {
          const { val, ovf } = evalSym(stmt.expr, s.store);
          const store: SymStore = new Map(s.store);
          store.set(stmt.name, val);
          return { pc: s.pc, store, ovf: or(s.ovf, ovf) };
        });

      case "if": {
        const thenStates: SymState[] = [];
        const elseStates: SymState[] = [];
        for (const s of states) {
          const { val, ovf } = evalSym(stmt.cond, s.store);
          const c = truthy(val);
          const ovf2 = or(s.ovf, ovf);
          const pcThen = and(s.pc, c);
          const pcElse = and(s.pc, not(c));
          recordEdge(`if#${stmt.id}:then`, pcThen);
          recordEdge(`if#${stmt.id}:else`, pcElse);
          if (sat(pcThen)) thenStates.push({ pc: pcThen, store: s.store, ovf: ovf2 });
          if (sat(pcElse)) elseStates.push({ pc: pcElse, store: s.store, ovf: ovf2 });
        }
        return [...execBlock(stmt.then, thenStates), ...execBlock(stmt.els, elseStates)];
      }

      case "while": {
        let head = states;
        const out: SymState[] = [];
        for (let i = 0; ; i++) {
          head = mergeStates(head); // 循环头:同一位置的合并点
          if (head.length === 0) return out;
          const enter: SymState[] = [];
          for (const s of head) {
            const { val, ovf } = evalSym(stmt.cond, s.store);
            const c = truthy(val);
            const ovf2 = or(s.ovf, ovf);
            const pcEnter = and(s.pc, c);
            const pcExit = and(s.pc, not(c));
            recordEdge(`while#${stmt.id}:exit`, pcExit);
            if (sat(pcExit)) out.push({ pc: pcExit, store: s.store, ovf: ovf2 });
            if (sat(pcEnter)) enter.push({ pc: pcEnter, store: s.store, ovf: ovf2 });
          }
          if (enter.length === 0) return out;
          if (i >= stmt.bound) {
            // 有界循环:超界路径被截断并记录
            for (const s of enter) {
              recordEdge(`while#${stmt.id}:bound`, s.pc);
              analysis.boundExceeded.push(s.pc);
              analysis.overflowConds.push(and(s.pc, s.ovf));
            }
            return out;
          }
          for (const s of enter) recordEdge(`while#${stmt.id}:enter`, s.pc);
          head = execBlock(stmt.body, enter);
        }
      }

      case "assert": {
        const pass: SymState[] = [];
        for (const s of states) {
          const { val, ovf } = evalSym(stmt.cond, s.store);
          const c = truthy(val);
          const ovf2 = or(s.ovf, ovf);
          const failCond = and(s.pc, not(c));
          const passCond = and(s.pc, c);
          recordEdge(`assert#${stmt.id}:fail`, failCond);
          recordEdge(`assert#${stmt.id}:pass`, passCond);
          if (sat(failCond)) {
            const list = analysis.failures.get(stmt.id);
            if (list) list.push(failCond);
            else analysis.failures.set(stmt.id, [failCond]);
            analysis.overflowConds.push(and(failCond, ovf2));
          }
          if (sat(passCond)) pass.push({ pc: passCond, store: s.store, ovf: ovf2 });
        }
        return pass;
      }
    }
  }

  const init: SymState = { pc: C(1), store: new Map(), ovf: C(0) };
  const finalStates = execBlock(prog.body, [init]);
  for (const s of finalStates) analysis.overflowConds.push(and(s.pc, s.ovf));
  return analysis;
}
