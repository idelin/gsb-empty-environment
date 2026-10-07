// interp.ts — independent tree-walking interpreter with closure identity.

import { BindingId, Expr, PrimOp } from './core';

export type Value = number | boolean | Closure | Native;

export interface Closure {
  tag: 'closure';
  name: string;
  bind: BindingId;
  body: Expr;
  env: Env;
}

export interface Native {
  tag: 'native';
  name: string;
  fn: (v: Value) => Value;
}

export type Env = Map<BindingId, Value>;

export class ReturnSignal {
  constructor(readonly value: Value) {}
}

export class EvalError extends Error {}

export function evalExpr(e: Expr, env: Env): Value {
  switch (e.k) {
    case 'lit':
      return e.v;
    case 'var': {
      const v = env.get(e.ref);
      if (v === undefined && !env.has(e.ref)) throw new EvalError(`unbound variable ${e.name}#${e.ref}`);
      return v as Value;
    }
    case 'lam':
      return { tag: 'closure', name: e.param, bind: e.bind, body: e.body, env };
    case 'app': {
      const f = evalExpr(e.fn, env);
      const a = evalExpr(e.arg, env);
      return apply(f, a);
    }
    case 'let': {
      const v = evalExpr(e.value, env);
      const env2 = new Map(env);
      env2.set(e.bind, v);
      return evalExpr(e.body, env2);
    }
    case 'if': {
      const c = evalExpr(e.c, env);
      if (typeof c !== 'boolean') throw new EvalError('if: condition must be boolean');
      return evalExpr(c ? e.t : e.e, env);
    }
    case 'prim':
      return evalPrim(e.op, e.args.map((a) => evalExpr(a, env)));
    case 'return':
      throw new ReturnSignal(evalExpr(e.value, env));
  }
}

export function apply(f: Value, a: Value): Value {
  if (typeof f === 'object' && f.tag === 'closure') {
    const env2 = new Map(f.env);
    env2.set(f.bind, a);
    try {
      return evalExpr(f.body, env2);
    } catch (sig) {
      if (sig instanceof ReturnSignal) return sig.value; // return boundary = enclosing lambda
      throw sig;
    }
  }
  if (typeof f === 'object' && f.tag === 'native') return f.fn(a);
  throw new EvalError('attempt to call a non-function');
}

export function run(e: Expr, env: Env = new Map()): Value {
  try {
    return evalExpr(e, env);
  } catch (sig) {
    if (sig instanceof ReturnSignal) return sig.value;
    throw sig;
  }
}

function evalPrim(op: PrimOp, args: Value[]): Value {
  const num = (v: Value): number => {
    if (typeof v !== 'number') throw new EvalError(`${op}: expected number, got ${showValue(v)}`);
    return v;
  };
  switch (op) {
    case '+':
      return num(args[0]) + num(args[1]);
    case '-':
      return num(args[0]) - num(args[1]);
    case '*':
      return num(args[0]) * num(args[1]);
    case '<':
      return num(args[0]) < num(args[1]);
    case '=':
      return args[0] === args[1];
    case 'not': {
      if (typeof args[0] !== 'boolean') throw new EvalError('not: expected boolean');
      return !args[0];
    }
    case 'same?':
      // identity: closures/natives compare by object reference
      return args[0] === args[1];
  }
}

export function showValue(v: Value): string {
  if (typeof v === 'number' || typeof v === 'boolean') return String(v);
  if (v.tag === 'closure') return `<closure ${v.name}#${v.bind}>`;
  return `<native ${v.name}>`;
}
