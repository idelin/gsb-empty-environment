// Tree-walking interpreter.
//
// Evaluation rules that the optimizer must preserve:
//  - call arguments evaluate strictly, left to right;
//  - `if` is lazy: only the taken branch evaluates;
//  - `shared` evaluates its body at most once per run, on first dynamic use;
//  - every call is appended to the trace (before running its body, so a
//    throwing call still shows up); impure calls also bump their counter.

import type { Expr } from './ast.ts';
import { EvalError, FUNCTIONS, newContext } from './runtime.ts';
import type { EvalContext, TraceEntry } from './runtime.ts';

export function evaluate(e: Expr, ctx: EvalContext): number {
  switch (e.kind) {
    case 'lit':
      return e.value;
    case 'if':
      return evaluate(e.cond, ctx) !== 0 ? evaluate(e.then, ctx) : evaluate(e.else, ctx);
    case 'shared': {
      if (ctx.memo.has(e.id)) return ctx.memo.get(e.id) as number;
      const value = evaluate(e.expr, ctx);
      ctx.memo.set(e.id, value);
      return value;
    }
    case 'call': {
      const def = FUNCTIONS[e.fn];
      if (!def) throw new EvalError(`unknown function: ${e.fn}`);
      if (e.args.length !== def.arity) {
        throw new EvalError(`${e.fn}: expected ${def.arity} args, got ${e.args.length}`);
      }
      const args = e.args.map((a) => evaluate(a, ctx));
      ctx.trace.push({ fn: e.fn, args, impure: !def.pure });
      return def.impl(args, ctx);
    }
  }
}

export interface RunResult {
  ok: boolean;
  value?: number;
  error?: string;
  trace: TraceEntry[];
  counters: Record<string, number>;
}

export function run(e: Expr): RunResult {
  const ctx = newContext();
  const counters = () => Object.fromEntries([...ctx.counters.entries()].sort());
  try {
    const value = evaluate(e, ctx);
    return { ok: true, value, trace: ctx.trace, counters: counters() };
  } catch (err) {
    const error = err instanceof Error ? `${err.name}: ${err.message}` : String(err);
    return { ok: false, error, trace: ctx.trace, counters: counters() };
  }
}

export function impureTrace(result: RunResult): string {
  return JSON.stringify(result.trace.filter((t) => t.impure).map((t) => [t.fn, t.args]));
}
