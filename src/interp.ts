// Interpreter: strict left-to-right evaluation, lazy if/and/or,
// counted side effects recorded in a trace.

import { Expr } from './ast';

export class EvalError extends Error {}

export interface RunResult {
  value: number | null; // defined when error === null
  error: string | null; // defined when evaluation threw
  trace: string[];      // side-effect log, in execution order
}

interface Ctx {
  count: number;
  trace: string[];
}

type Env = ReadonlyMap<string, number>;

export function run(root: Expr): RunResult {
  const ctx: Ctx = { count: 0, trace: [] };
  try {
    const value = evalExpr(root, ctx, new Map());
    return { value, error: null, trace: ctx.trace };
  } catch (err) {
    if (err instanceof EvalError) {
      return { value: null, error: err.message, trace: ctx.trace };
    }
    throw err;
  }
}

function evalExpr(e: Expr, ctx: Ctx, env: Env): number {
  switch (e.tag) {
    case 'lit':
      return e.value;
    case 'var': {
      const v = env.get(e.name);
      if (v === undefined) throw new EvalError(`unbound variable: ${e.name}`);
      return v;
    }
    case 'pure': {
      const args = e.args.map((a) => evalExpr(a, ctx, env));
      return applyPure(e.op, args);
    }
    case 'eff': {
      // tick(x): bump the counter, log, return x.
      const x = evalExpr(e.args[0], ctx, env);
      ctx.count += 1;
      ctx.trace.push(`tick#${ctx.count}(${x})`);
      return x;
    }
    case 'if':
      return evalExpr(e.cond, ctx, env) !== 0
        ? evalExpr(e.then, ctx, env)
        : evalExpr(e.els, ctx, env);
    case 'and': {
      const l = evalExpr(e.lhs, ctx, env);
      return l === 0 ? 0 : evalExpr(e.rhs, ctx, env);
    }
    case 'or': {
      const l = evalExpr(e.lhs, ctx, env);
      return l !== 0 ? l : evalExpr(e.rhs, ctx, env);
    }
    case 'let': {
      const v = evalExpr(e.value, ctx, env);
      const env2 = new Map(env);
      env2.set(e.name, v);
      return evalExpr(e.body, ctx, env2);
    }
  }
}

// Error messages are built from runtime argument values, not from the
// expression text, so CSE (which rewrites expressions into variables)
// never changes the observable error of a given failing call.
function applyPure(op: string, args: number[]): number {
  switch (op) {
    case 'add': return args[0] + args[1];
    case 'sub': return args[0] - args[1];
    case 'mul': return args[0] * args[1];
    case 'div':
      if (args[1] === 0) throw new EvalError(`division by zero: div(${args[0]}, ${args[1]})`);
      return args[0] / args[1];
    case 'mod':
      if (args[1] === 0) throw new EvalError(`division by zero: mod(${args[0]}, ${args[1]})`);
      return args[0] % args[1];
    case 'neg': return -args[0];
    case 'abs': return Math.abs(args[0]);
    case 'min': return Math.min(args[0], args[1]);
    case 'max': return Math.max(args[0], args[1]);
    default: throw new Error(`unknown pure op: ${op}`);
  }
}
