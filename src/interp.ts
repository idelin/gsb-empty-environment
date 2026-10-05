/**
 * Independent concrete interpreter for bit-vector expressions.
 *
 * This interpreter shares no code with the symbolic (SAT) path, so it can be
 * used to cross-validate counterexamples and to brute-force small widths in
 * tests.
 */

import type { Expr } from './expr.ts';

export function maskOf(width: number): bigint {
  return (1n << BigInt(width)) - 1n;
}

export function truncate(value: bigint, width: number): bigint {
  return value & maskOf(width);
}

/** True when the unsigned `width`-bit value has its sign bit set. */
function toSigned(value: bigint, width: number): bigint {
  const signBit = 1n << BigInt(width - 1);
  return (value & signBit) !== 0n ? value - (1n << BigInt(width)) : value;
}

export function evaluate(
  e: Expr,
  env: ReadonlyMap<string, bigint>,
  width: number,
): bigint {
  const mask = maskOf(width);
  switch (e.kind) {
    case 'var': {
      const value = env.get(e.name);
      if (value === undefined) {
        throw new Error(`unbound variable: ${e.name}`);
      }
      return value & mask;
    }
    case 'const':
      return e.value & mask;
    case 'not':
      return ~evaluate(e.arg, env, width) & mask;
    case 'ite':
      return evaluate(e.cond, env, width) !== 0n
        ? evaluate(e.then, env, width)
        : evaluate(e.otherwise, env, width);
    case 'bin': {
      const lhs = evaluate(e.lhs, env, width);
      const rhs = evaluate(e.rhs, env, width);
      switch (e.op) {
        case 'and':
          return lhs & rhs;
        case 'or':
          return lhs | rhs;
        case 'xor':
          return lhs ^ rhs;
        case 'add':
          return (lhs + rhs) & mask;
        case 'sub':
          return (lhs - rhs) & mask;
        case 'shl':
          // Fixed semantics: shift amount >= width yields zero.
          return rhs >= BigInt(width) ? 0n : (lhs << rhs) & mask;
        case 'lshr':
          return rhs >= BigInt(width) ? 0n : lhs >> rhs;
        case 'ashr': {
          if (rhs >= BigInt(width)) return 0n;
          return truncate(toSigned(lhs, width) >> rhs, width);
        }
      }
    }
  }
}
