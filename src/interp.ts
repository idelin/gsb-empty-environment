// 具体解释器：与符号执行器完全相同的语义（回绕、非短路逻辑、循环上界）。
// 用于全输入穷举对照。

import type { Expr, Program, Stmt } from "./ast.ts";
import { wrap8 } from "./ast.ts";
import { arith, cmpOp, overflows } from "./sym.ts";
import type { Env } from "./sym.ts";

export interface ConcResult {
  failedAsserts: number[];
  overflowSites: Set<number>;
  branches: Set<string>;
  boundExceeded: boolean;
}

export function runConcrete(prog: Program, env: Env): ConcResult {
  const res: ConcResult = {
    failedAsserts: [],
    overflowSites: new Set(),
    branches: new Set(),
    boundExceeded: false,
  };
  execBlockC(prog.body, new Map(), prog, env, res);
  return res;
}

function evalE(e: Expr, vars: Map<string, number>, env: Env, res: ConcResult): number {
  switch (e.kind) {
    case "num":
      return e.value;
    case "var":
      return vars.get(e.name) ?? 0;
    case "input":
      return env[e.name];
    case "neg":
      return wrap8(-evalE(e.e, vars, env, res));
    case "lnot":
      return evalE(e.e, vars, env, res) === 0 ? 1 : 0;
    case "land":
      return evalE(e.l, vars, env, res) !== 0 && evalE(e.r, vars, env, res) !== 0 ? 1 : 0;
    case "lor":
      return evalE(e.l, vars, env, res) !== 0 || evalE(e.r, vars, env, res) !== 0 ? 1 : 0;
    case "bin": {
      const l = evalE(e.l, vars, env, res);
      const r = evalE(e.r, vars, env, res);
      switch (e.op) {
        case "add":
        case "sub":
        case "mul":
          if (overflows(e.op, l, r)) res.overflowSites.add(e.id);
          return wrap8(arith(e.op, l, r));
        case "and":
        case "or":
        case "xor":
          return arith(e.op, l, r);
        default:
          return cmpOp(e.op, l, r) ? 1 : 0;
      }
    }
  }
}

// 返回 false 表示因循环越界而中止（该路径不参与后续断言判定）。
function execBlockC(
  stmts: Stmt[],
  vars: Map<string, number>,
  prog: Program,
  env: Env,
  res: ConcResult,
): boolean {
  for (const st of stmts) {
    if (!execStmtC(st, vars, prog, env, res)) return false;
  }
  return true;
}

function execStmtC(
  st: Stmt,
  vars: Map<string, number>,
  prog: Program,
  env: Env,
  res: ConcResult,
): boolean {
  switch (st.kind) {
    case "assign":
      vars.set(st.name, evalE(st.expr, vars, env, res));
      return true;
    case "assert":
      if (evalE(st.cond, vars, env, res) === 0) res.failedAsserts.push(st.id);
      return true;
    case "if": {
      const c = evalE(st.cond, vars, env, res);
      if (c !== 0) {
        res.branches.add(`${st.id}:then`);
        return execBlockC(st.then, vars, prog, env, res);
      }
      res.branches.add(`${st.id}:else`);
      return execBlockC(st.els, vars, prog, env, res);
    }
    case "while": {
      let i = 0;
      while (evalE(st.cond, vars, env, res) !== 0) {
        res.branches.add(`${st.id}:body`);
        if (i >= prog.loopBound) {
          res.boundExceeded = true;
          return false;
        }
        if (!execBlockC(st.body, vars, prog, env, res)) return false;
        i++;
      }
      res.branches.add(`${st.id}:exit`);
      return true;
    }
  }
}
