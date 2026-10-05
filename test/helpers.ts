/**
 * Shared brute-force helpers for tests.
 *
 * The checker is validated by *exhaustive* enumeration at small bit widths:
 * every input combination is tried, so the expected verdict and the expected
 * minimal counterexample are known exactly. No random testing is used —
 * randomness cannot stand in for a proof.
 */

import { collectVars, type Expr } from '../src/expr.ts';
import { evaluate } from '../src/interp.ts';

export function sortedVars(...exprs: Expr[]): string[] {
  const set = new Set<string>();
  for (const e of exprs) collectVars(e, set);
  return [...set].sort();
}

/**
 * Enumerate all inputs in lexicographic order (variables sorted by name,
 * each compared as an unsigned value) and return the first assignment where
 * the two expressions differ, or null when they are equivalent.
 */
export function bruteForceCounterexample(
  lhs: Expr,
  rhs: Expr,
  width: number,
): Record<string, bigint> | null {
  const names = sortedVars(lhs, rhs);
  const limit = Number(1n << BigInt(width));
  const total = limit ** names.length;
  const values = new Array<bigint>(names.length).fill(0n);
  for (let n = 0; n < total; n++) {
    let rem = n;
    for (let i = names.length - 1; i >= 0; i--) {
      values[i] = BigInt(rem % limit);
      rem = Math.floor(rem / limit);
    }
    const env = new Map(names.map((name, i) => [name, values[i]]));
    if (evaluate(lhs, env, width) !== evaluate(rhs, env, width)) {
      return Object.fromEntries(env);
    }
  }
  return null;
}

export function structuralKey(e: Expr): string {
  return JSON.stringify(e, (_key, value) =>
    typeof value === 'bigint' ? `${value}n` : value,
  );
}
