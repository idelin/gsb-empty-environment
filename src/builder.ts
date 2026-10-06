// Concise constructors for writing test programs directly as ASTs.

import { Expr, Stmt, Program, BinOp } from './ast';

export const num = (value: number): Expr => ({ kind: 'num', value });
export const bool = (value: boolean): Expr => ({ kind: 'bool', value });
export const vr = (name: string): Expr => ({ kind: 'var', name });
export const bin = (op: BinOp, left: Expr, right: Expr): Expr => ({ kind: 'bin', op, left, right });
export const not = (expr: Expr): Expr => ({ kind: 'not', expr });
export const call = (fn: Expr, ...args: Expr[]): Expr => ({ kind: 'call', fn, args });
export const fn = (params: string[], body: Stmt[], selfName: string | null = null): Expr => ({
  kind: 'fn',
  selfName,
  params,
  body,
});
export const arr = (...items: Expr[]): Expr => ({ kind: 'arr', items });
export const idx = (a: Expr, i: Expr): Expr => ({ kind: 'index', arr: a, index: i });
export const push = (a: Expr, value: Expr): Expr => ({ kind: 'push', arr: a, value });
export const len = (a: Expr): Expr => ({ kind: 'len', arr: a });

export const letm = (name: string, init: Expr): Stmt => ({ kind: 'let', mutable: true, name, init });
export const letc = (name: string, init: Expr): Stmt => ({ kind: 'let', mutable: false, name, init });
export const set = (name: string, value: Expr): Stmt => ({ kind: 'set', name, value });
export const expr = (e: Expr): Stmt => ({ kind: 'expr', expr: e });
export const block = (...body: Stmt[]): Stmt => ({ kind: 'block', body });
export const ifs = (cond: Expr, then: Stmt[], els: Stmt[] = []): Stmt => ({
  kind: 'if',
  cond,
  then,
  else: els,
});
export const whiles = (cond: Expr, body: Stmt[]): Stmt => ({ kind: 'while', cond, body });
export const ret = (e: Expr | null = null): Stmt => ({ kind: 'return', expr: e });
export const print = (e: Expr): Stmt => ({ kind: 'print', expr: e });
export const prog = (...body: Stmt[]): Program => ({ body });
