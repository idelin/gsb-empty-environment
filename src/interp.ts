// Tree-walking interpreter. Closures carry a unique runtime identity so tests
// can observe that a captured closure passed into an extracted function is the
// very same object, not a re-created one. `return` unwinds to the nearest
// enclosing function application via an internal signal.

import type { Binder, BindId, Expr, NodeId, Program } from './ast.ts';

export type Value = number | boolean | ClosureVal;

export interface ClosureVal {
  tag: 'closure';
  id: number; // runtime allocation identity
  defNode: NodeId | null; // defining lam node (null for top-level decls)
  params: Binder[];
  body: Expr;
  env: Map<BindId, Value>;
}

export type TraceEvent =
  | { type: 'alloc'; closureId: number; defNode: NodeId | null }
  | { type: 'apply'; closureId: number; defNode: NodeId | null };

export interface Tracer {
  events: TraceEvent[];
}

class ReturnSignal {
  value: Value;
  constructor(value: Value) {
    this.value = value;
  }
}

interface Ctx {
  tracer?: Tracer;
  nextClosureId: number;
}

function makeClosure(
  ctx: Ctx,
  defNode: NodeId | null,
  params: Binder[],
  body: Expr,
  env: Map<BindId, Value>,
): ClosureVal {
  const c: ClosureVal = { tag: 'closure', id: ctx.nextClosureId++, defNode, params, body, env };
  ctx.tracer?.events.push({ type: 'alloc', closureId: c.id, defNode });
  return c;
}

function apply(ctx: Ctx, fn: Value, args: Value[]): Value {
  if (typeof fn !== 'object') throw new Error('applying a non-function');
  if (fn.params.length !== args.length) {
    throw new Error(
      `arity mismatch: expected ${fn.params.length}, got ${args.length}`,
    );
  }
  ctx.tracer?.events.push({ type: 'apply', closureId: fn.id, defNode: fn.defNode });
  const env = new Map(fn.env);
  fn.params.forEach((p, i) => env.set(p.bind, args[i]));
  try {
    return evalExpr(ctx, fn.body, env);
  } catch (e) {
    if (e instanceof ReturnSignal) return e.value; // control boundary: nearest function
    throw e;
  }
}

export function evalExpr(ctx: Ctx, e: Expr, env: Map<BindId, Value>): Value {
  switch (e.kind) {
    case 'num':
    case 'bool':
      return e.value;
    case 'var': {
      const v = env.get(e.ref);
      if (v === undefined) throw new Error(`unbound variable ${e.name}#${e.ref}`);
      return v;
    }
    case 'lam':
      return makeClosure(ctx, e.id, e.params, e.body, env);
    case 'app': {
      const fn = evalExpr(ctx, e.fn, env);
      const args = e.args.map((a) => evalExpr(ctx, a, env));
      return apply(ctx, fn, args);
    }
    case 'let': {
      const v = evalExpr(ctx, e.value, env);
      const env2 = new Map(env);
      env2.set(e.binder.bind, v);
      return evalExpr(ctx, e.body, env2);
    }
    case 'if': {
      const c = evalExpr(ctx, e.cond, env);
      if (typeof c !== 'boolean') throw new Error('if condition must be boolean');
      return evalExpr(ctx, c ? e.then : e.els, env);
    }
    case 'bin': {
      const l = evalExpr(ctx, e.lhs, env);
      const r = evalExpr(ctx, e.rhs, env);
      switch (e.op) {
        case 'add':
        case 'sub':
        case 'mul':
        case 'lt': {
          if (typeof l !== 'number' || typeof r !== 'number')
            throw new Error(`${e.op} expects numbers`);
          if (e.op === 'add') return l + r;
          if (e.op === 'sub') return l - r;
          if (e.op === 'mul') return l * r;
          return l < r;
        }
        case 'eq':
          if (typeof l !== typeof r || typeof l === 'object')
            throw new Error('eq expects two first-order values of the same type');
          return l === r;
        case 'and':
        case 'or':
          if (typeof l !== 'boolean' || typeof r !== 'boolean')
            throw new Error(`${e.op} expects booleans`);
          return e.op === 'and' ? l && r : l || r;
      }
      throw new Error('unreachable');
    }
    case 'return':
      throw new ReturnSignal(evalExpr(ctx, e.value, env));
  }
}

export function evaluate(
  root: Expr,
  env: Map<BindId, Value> = new Map(),
  tracer?: Tracer,
): Value {
  const ctx: Ctx = { tracer, nextClosureId: 0 };
  try {
    return evalExpr(ctx, root, env);
  } catch (e) {
    if (e instanceof ReturnSignal) throw new Error('return escaped to top level');
    throw e;
  }
}

export function evaluateProgram(
  program: Program,
  env: Map<BindId, Value> = new Map(),
  tracer?: Tracer,
): Value {
  const ctx: Ctx = { tracer, nextClosureId: 0 };
  const full = new Map(env);
  for (const fn of program.functions) {
    // Extracted functions are closed by construction (free vars became params).
    full.set(fn.bind, makeClosure(ctx, null, fn.params, fn.body, new Map()));
  }
  try {
    return evalExpr(ctx, program.root, full);
  } catch (e) {
    if (e instanceof ReturnSignal) throw new Error('return escaped to top level');
    throw e;
  }
}
