// Exhaustive checker: enumerates the whole input space with the independent
// interpreter. This is a proof (not sampling) and is the reference used to
// validate the symbolic engine at small bit widths.

import { collectVars } from './ast.ts';
import type { Expr } from './ast.ts';
import { evaluate } from './interp.ts';
import type { CheckResult } from './result.ts';

export const DEFAULT_EXHAUSTIVE_MAX_BITS = 20;

export function exhaustiveCheck(
  a: Expr,
  b: Expr,
  width: number,
  maxBits: number = DEFAULT_EXHAUSTIVE_MAX_BITS,
): CheckResult {
  const vars = collectVars(a, b);
  const totalBits = vars.length * width;
  if (totalBits > maxBits) {
    return {
      status: 'unknown',
      reason: `exhaustive space 2^${totalBits} exceeds limit 2^${maxBits}`,
    };
  }
  const total = 1n << BigInt(totalBits);
  const mask = (1n << BigInt(width)) - 1n;
  // Enumeration order is lexicographic over the variable tuple (sorted by
  // name, first variable most significant), so the first mismatch found is
  // the lexicographically smallest counterexample.
  for (let idx = 0n; idx < total; idx++) {
    const env: Record<string, bigint> = {};
    for (let k = 0; k < vars.length; k++) {
      const shift = BigInt((vars.length - 1 - k) * width);
      env[vars[k]] = (idx >> shift) & mask;
    }
    if (evaluate(a, env, width) !== evaluate(b, env, width)) {
      return { status: 'not-equal', counterexample: env };
    }
  }
  return { status: 'equal' };
}
