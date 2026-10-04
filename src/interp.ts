// Independent concrete interpreter over BigInt.
// All results are truncated to `width` bits (unsigned, wrap-around).
// Shift semantics: shift amount >= width yields 0 (this covers the required
// "amount > width => 0" rule; amount == width is naturally 0 for logical
// shifts, so the behaviour is fixed and total for every input).

import type { Expr } from './ast.ts';

export type Env = Readonly<Record<string, bigint>>;

export function maskOf(width: number): bigint {
  return (1n << BigInt(width)) - 1n;
}

export function truncate(value: bigint, width: number): bigint {
  return value & maskOf(width);
}

export function evaluate(e: Expr, env: Env, width: number): bigint {
  if (!Number.isInteger(width) || width < 1) throw new Error(`bad width ${width}`);
  const mask = maskOf(width);
  const w = BigInt(width);
  const ev = (x: Expr): bigint => evalRec(x, env, width, mask, w);
  return ev(e);
}

function evalRec(e: Expr, env: Env, width: number, mask: bigint, w: bigint): bigint {
  switch (e.kind) {
    case 'const':
      return e.value & mask;
    case 'var': {
      const v = env[e.name];
      if (v === undefined) throw new Error(`unbound variable '${e.name}'`);
      return v & mask;
    }
    case 'not':
      return ~evalRec(e.a, env, width, mask, w) & mask;
    case 'neg':
      return -evalRec(e.a, env, width, mask, w) & mask;
    case 'ite':
      return evalRec(e.c, env, width, mask, w) !== 0n
        ? evalRec(e.t, env, width, mask, w)
        : evalRec(e.e, env, width, mask, w);
    case 'bin': {
      const a = evalRec(e.a, env, width, mask, w);
      const b = evalRec(e.b, env, width, mask, w);
      switch (e.op) {
        case 'and': return a & b;
        case 'or': return a | b;
        case 'xor': return a ^ b;
        case 'add': return (a + b) & mask;
        case 'sub': return (a - b) & mask;
        case 'shl': return b >= w ? 0n : (a << b) & mask;
        case 'shr': return b >= w ? 0n : a >> b;
      }
    }
  }
}
