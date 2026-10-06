// Unoptimized reference interpreter: plain recursive evaluation, every call
// (tail or not) pushes a new frame. Used to cross-check the TCO interpreter
// on small programs (results and call order) and to contrast space usage.

import { Expr, FunDef, Program } from './lang';
import {
  Env,
  EventLog,
  FrameInfo,
  TailRecord,
  TraceSource,
  Value,
  applyPrim,
  raiseError,
  showValue,
} from './runtime';

export interface NaiveOptions {
  log?: EventLog;
}

class Ctx implements TraceSource {
  readonly stack: FrameInfo[] = [];
  constructor(readonly defs: Map<string, FunDef>, readonly log?: EventLog) {}
  snapshotStack(): FrameInfo[] {
    return this.stack.map((f) => ({ fnName: f.fnName, args: [...f.args] }));
  }
  snapshotTail(): TailRecord[] {
    return [];
  }
  tailCallCount(): number {
    return 0;
  }
}

function apply(fn: FunDef, args: Value[], ctx: Ctx): Value {
  ctx.stack.push({ fnName: fn.name, args: [...args] });
  ctx.log?.enter(fn.name, args);
  try {
    const env = new Env(null);
    for (let i = 0; i < fn.params.length; i++) env.define(fn.params[i]!, args[i]!);
    const value = evalExpr(fn.body, env, ctx);
    ctx.log?.exit(fn.name, value);
    return value;
  } finally {
    ctx.stack.pop();
  }
}

function evalExpr(e: Expr, env: Env, ctx: Ctx): Value {
  switch (e.kind) {
    case 'num':
    case 'bool':
    case 'str':
      return e.value;
    case 'var': {
      const v = env.lookup(e.name);
      if (v === undefined) raiseError(`unbound variable '${e.name}'`, ctx);
      return v;
    }
    case 'if': {
      const cond = evalExpr(e.cond, env, ctx);
      if (typeof cond !== 'boolean') {
        raiseError(`if condition must be boolean, got ${showValue(cond)}`, ctx);
      }
      return evalExpr(cond ? e.then : e.else, env, ctx);
    }
    case 'begin': {
      let value: Value = false;
      for (const x of e.exprs) value = evalExpr(x, env, ctx);
      return value;
    }
    case 'let': {
      const v = evalExpr(e.value, env, ctx);
      const inner = new Env(env);
      inner.define(e.name, v);
      return evalExpr(e.body, inner, ctx);
    }
    case 'error': {
      const msg = evalExpr(e.message, env, ctx);
      raiseError(typeof msg === 'string' ? msg : showValue(msg), ctx);
    }
    case 'prim': {
      if (e.op === 'and' || e.op === 'or') {
        for (const a of e.args) {
          const v = evalExpr(a, env, ctx);
          if (typeof v !== 'boolean') {
            raiseError(`'${e.op}' expects boolean operands, got ${showValue(v)}`, ctx);
          }
          if (e.op === 'and' && !v) return false;
          if (e.op === 'or' && v) return true;
        }
        return e.op === 'and';
      }
      const args = e.args.map((a) => evalExpr(a, env, ctx));
      return applyPrim(e.op, args, ctx);
    }
    case 'call': {
      const fn = ctx.defs.get(e.name);
      if (fn === undefined) raiseError(`unknown function '${e.name}'`, ctx);
      if (fn.params.length !== e.args.length) {
        raiseError(
          `arity mismatch: '${e.name}' expects ${fn.params.length} argument(s), got ${e.args.length}`,
          ctx,
        );
      }
      const args = e.args.map((a) => evalExpr(a, env, ctx));
      return apply(fn, args, ctx);
    }
  }
}

export function runNaive(program: Program, opts: NaiveOptions = {}): Value {
  const defs = new Map<string, FunDef>();
  for (const d of program.defs) defs.set(d.name, d);
  const ctx = new Ctx(defs, opts.log);
  const globalEnv = new Env(null);
  let value: Value = false;
  for (const e of program.main) value = evalExpr(e, globalEnv, ctx);
  return value;
}
