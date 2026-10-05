// 符号执行器。
//
// 状态 = { vars: 变量 -> 符号表达式, pc: 路径条件（合取列表） }。
// 到达同一程序位置的状态被合并为一个状态：每个变量取
//   ite(路径条件_A, 值_A, 值_B)
// 合并后路径条件为各分支路径条件的析取 —— 各条路径的条件以 ite 守卫的
// 形式完整保留在符号值中，不会丢失。
//
// 执行过程中同时记录：
// - 每个断言的失败条件（路径条件 ∧ ¬断言条件）；
// - 每个分支臂的可满足性公式（用于不可达分支检查）；
// - 每个算术表达式的溢出公式（路径条件 ∧ 溢出谓词）；
// - 循环上界耗尽仍未退出的路径（boundExceeded）。

import type { Expr, Program, Stmt } from "./ast.ts";
import type { Sym } from "./sym.ts";
import {
  C0,
  sAnd,
  sAndAll,
  sBin,
  sBool,
  sCmp,
  sIn,
  sIte,
  sNot,
  sOr,
  sOrAll,
  sOvf,
  sc,
  symEq,
} from "./sym.ts";

export interface SState {
  vars: Map<string, Sym>;
  pc: Sym[];
}

export interface AssertRecord {
  id: number;
  msg: string;
  formula: Sym;
}
export interface BranchRecord {
  key: string;
  desc: string;
  formula: Sym;
}
export interface OverflowRecord {
  exprId: number;
  op: string;
  formula: Sym;
}

export interface ExecReport {
  asserts: AssertRecord[];
  branches: BranchRecord[];
  overflows: OverflowRecord[];
  boundExceeded: Sym;
  badFormula: Sym;
  finalStates: SState[];
  statesCreated: number;
  statesMerged: number;
}

interface ExecCtx {
  asserts: Map<number, AssertRecord>;
  branches: Map<string, BranchRecord>;
  overflows: Map<number, OverflowRecord>;
  boundExceeded: Sym;
  statesCreated: number;
  statesMerged: number;
}

export function execute(prog: Program): ExecReport {
  const ctx: ExecCtx = {
    asserts: new Map(),
    branches: new Map(),
    overflows: new Map(),
    boundExceeded: C0,
    statesCreated: 1,
    statesMerged: 0,
  };
  const init: SState = { vars: new Map(), pc: [] };
  const finalStates = execBlock(prog.body, [init], prog, ctx);
  const asserts = [...ctx.asserts.values()];
  return {
    asserts,
    branches: [...ctx.branches.values()],
    overflows: [...ctx.overflows.values()],
    boundExceeded: ctx.boundExceeded,
    badFormula: sOrAll(asserts.map((a) => a.formula)),
    finalStates,
    statesCreated: ctx.statesCreated,
    statesMerged: ctx.statesMerged,
  };
}

function addBranch(ctx: ExecCtx, key: string, desc: string, f: Sym): void {
  const rec = ctx.branches.get(key);
  if (rec) rec.formula = sOr(rec.formula, f);
  else ctx.branches.set(key, { key, desc, formula: f });
}

function addOverflow(ctx: ExecCtx, exprId: number, op: string, f: Sym): void {
  const rec = ctx.overflows.get(exprId);
  if (rec) rec.formula = sOr(rec.formula, f);
  else ctx.overflows.set(exprId, { exprId, op, formula: f });
}

function symOf(e: Expr, s: SState, ctx: ExecCtx): Sym {
  switch (e.kind) {
    case "num":
      return sc(e.value);
    case "var":
      return s.vars.get(e.name) ?? C0;
    case "input":
      return sIn(e.name);
    case "neg":
      return sBin("sub", C0, symOf(e.e, s, ctx));
    case "lnot":
      return sNot(symOf(e.e, s, ctx));
    case "land":
      return sAnd(symOf(e.l, s, ctx), symOf(e.r, s, ctx));
    case "lor":
      return sOr(symOf(e.l, s, ctx), symOf(e.r, s, ctx));
    case "bin": {
      const l = symOf(e.l, s, ctx);
      const r = symOf(e.r, s, ctx);
      switch (e.op) {
        case "add":
        case "sub":
        case "mul":
          addOverflow(ctx, e.id, e.op, sAndAll([...s.pc, sOvf(e.op, l, r)]));
          return sBin(e.op, l, r);
        case "and":
        case "or":
        case "xor":
          return sBin(e.op, l, r);
        default:
          return sCmp(e.op, l, r);
      }
    }
  }
}

function execBlock(stmts: Stmt[], states: SState[], prog: Program, ctx: ExecCtx): SState[] {
  let cur = states;
  for (const st of stmts) {
    if (cur.length === 0) break;
    cur = execStmt(st, cur, prog, ctx);
  }
  return cur;
}

function execStmt(st: Stmt, states: SState[], prog: Program, ctx: ExecCtx): SState[] {
  switch (st.kind) {
    case "assign":
      return states.map((s) => {
        const vars = new Map(s.vars);
        vars.set(st.name, symOf(st.expr, s, ctx));
        return { vars, pc: s.pc };
      });
    case "assert": {
      for (const s of states) {
        const cond = symOf(st.cond, s, ctx);
        const fail = sAndAll([...s.pc, sNot(cond)]);
        const rec = ctx.asserts.get(st.id);
        if (rec) rec.formula = sOr(rec.formula, fail);
        else ctx.asserts.set(st.id, { id: st.id, msg: st.msg, formula: fail });
      }
      return states;
    }
    case "if": {
      const thenS: SState[] = [];
      const elseS: SState[] = [];
      for (const s of states) {
        const cond = symOf(st.cond, s, ctx);
        addBranch(ctx, `${st.id}:then`, `if#${st.id} then`, sAndAll([...s.pc, cond]));
        addBranch(ctx, `${st.id}:else`, `if#${st.id} else`, sAndAll([...s.pc, sNot(cond)]));
        thenS.push({ vars: s.vars, pc: [...s.pc, sBool(cond)] });
        elseS.push({ vars: s.vars, pc: [...s.pc, sNot(cond)] });
        ctx.statesCreated += 2;
      }
      const rt = execBlock(st.then, thenS, prog, ctx);
      const re = execBlock(st.els, elseS, prog, ctx);
      return mergeStates([...rt, ...re], ctx);
    }
    case "while": {
      const exits: SState[] = [];
      let cur: SState[] = states;
      for (let i = 0; i < prog.loopBound; i++) {
        const enter: SState[] = [];
        for (const s of cur) {
          const cond = symOf(st.cond, s, ctx);
          addBranch(ctx, `${st.id}:body`, `while#${st.id} body`, sAndAll([...s.pc, cond]));
          addBranch(ctx, `${st.id}:exit`, `while#${st.id} exit`, sAndAll([...s.pc, sNot(cond)]));
          enter.push({ vars: s.vars, pc: [...s.pc, sBool(cond)] });
          exits.push({ vars: s.vars, pc: [...s.pc, sNot(cond)] });
          ctx.statesCreated += 2;
        }
        cur = mergeStates(enter, ctx);
        if (cur.length === 0) break;
        cur = mergeStates(execBlock(st.body, cur, prog, ctx), ctx);
      }
      if (cur.length > 0) {
        // 第 loopBound+1 次条件求值：仍为真则越界，为假则正常退出。
        const stillEnter: Sym[] = [];
        for (const s of cur) {
          const cond = symOf(st.cond, s, ctx);
          addBranch(ctx, `${st.id}:body`, `while#${st.id} body`, sAndAll([...s.pc, cond]));
          addBranch(ctx, `${st.id}:exit`, `while#${st.id} exit`, sAndAll([...s.pc, sNot(cond)]));
          exits.push({ vars: s.vars, pc: [...s.pc, sNot(cond)] });
          stillEnter.push(sAndAll([...s.pc, cond]));
        }
        ctx.boundExceeded = sOr(ctx.boundExceeded, sOrAll(stillEnter));
      }
      return mergeStates(exits, ctx);
    }
  }
}

// 合并同一程序位置上的所有状态；路径条件不可满足的状态被剪枝。
export function mergeStates(states: SState[], ctx?: ExecCtx): SState[] {
  const live: { g: Sym; s: SState }[] = [];
  for (const s of states) {
    const g = sAndAll(s.pc);
    if (g.k === "c" && g.v === 0) continue;
    live.push({ g, s });
  }
  if (live.length === 0) return [];
  let accVars = live[0].s.vars;
  let accG = live[0].g;
  for (let i = 1; i < live.length; i++) {
    const { g, s } = live[i];
    const keys = new Set([...accVars.keys(), ...s.vars.keys()]);
    const merged = new Map<string, Sym>();
    for (const name of keys) {
      const va = accVars.get(name) ?? C0;
      const vb = s.vars.get(name) ?? C0;
      merged.set(name, symEq(va, vb) ? va : sIte(accG, va, vb));
    }
    accVars = merged;
    accG = sOr(accG, g);
    if (ctx) ctx.statesMerged++;
  }
  return [{ vars: accVars, pc: [accG] }];
}
