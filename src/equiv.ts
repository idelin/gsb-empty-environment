import type { Expr, Tri } from "./ast.ts";
import { evalExpr } from "./eval.ts";
import type { AttrDecl } from "./policy.ts";

export interface Mismatch {
  readonly env: Record<string, number | undefined>;
  readonly a: Tri;
  readonly b: Tri;
}

export interface EquivResult {
  readonly equivalent: boolean;
  readonly checked: number;
  readonly mismatches: readonly Mismatch[];
}

/**
 * Exhaustive equivalence tester: enumerates the full finite input domain
 * (every declared attribute range, plus "missing" for optional attributes)
 * and compares the three-valued results of both expressions on every input.
 */
export function checkEquivalence(
  attrs: readonly AttrDecl[],
  a: Expr,
  b: Expr,
  maxCases = 1_000_000,
): EquivResult {
  const domains: (number | undefined)[][] = attrs.map((d) => {
    const values: (number | undefined)[] = d.required ? [] : [undefined];
    for (let v = d.min; v <= d.max; v += 1) values.push(v);
    return values;
  });
  const total = domains.reduce((n, d) => n * d.length, 1);
  if (total > maxCases) {
    throw new Error(`finite domain too large to enumerate: ${total} cases (limit ${maxCases})`);
  }
  const mismatches: Mismatch[] = [];
  let checked = 0;
  const env: Record<string, number | undefined> = {};
  const recurse = (i: number): void => {
    if (i === attrs.length) {
      checked += 1;
      const ra = evalExpr(a, env);
      const rb = evalExpr(b, env);
      if (ra !== rb && mismatches.length < 20) {
        mismatches.push({ env: { ...env }, a: ra, b: rb });
      }
      return;
    }
    for (const v of domains[i]) {
      env[attrs[i].name] = v;
      recurse(i + 1);
    }
  };
  recurse(0);
  return { equivalent: mismatches.length === 0, checked, mismatches };
}
