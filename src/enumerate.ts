/**
 * Depth-bounded enumeration of values, used to cross-validate the symbolic
 * matrix algorithm. Values are generated round by round: round d produces
 * exactly the values of constructor depth d, built from constructor fields
 * whose values have depth < d with at least one field at depth d - 1.
 * Recursive types are therefore expanded only up to the depth bound.
 */

import { Ctx } from "./ctx";
import { Value } from "./types";

export interface EnumResult {
  /** All enumerated values per type, ordered by non-decreasing depth. */
  byType: Map<string, Value[]>;
  /** Depth of each enumerated value, aligned with `byType`. */
  depthOf: Map<string, number[]>;
  /** True if generation stopped early because of the value cap. */
  truncated: boolean;
  total: number;
}

/** Types reachable from the given roots (inclusive). */
export function reachableTypes(ctx: Ctx, roots: string[]): Set<string> {
  const seen = new Set<string>();
  const stack = [...roots];
  while (stack.length > 0) {
    const t = stack.pop()!;
    if (seen.has(t)) continue;
    seen.add(t);
    for (const c of ctx.env[t]!.ctors) stack.push(...c.fields);
  }
  return seen;
}

export function enumerateValues(
  ctx: Ctx,
  rootTypes: string[],
  maxDepth: number,
  cap = 100_000
): EnumResult {
  const types = reachableTypes(ctx, rootTypes);
  const byType = new Map<string, Value[]>();
  const depthOf = new Map<string, number[]>();
  for (const t of types) {
    byType.set(t, []);
    depthOf.set(t, []);
  }
  let total = 0;
  let truncated = false;

  for (let d = 1; d <= maxDepth && !truncated; d++) {
    for (const t of types) {
      for (const c of ctx.env[t]!.ctors) {
        if (c.fields.length === 0) {
          if (d !== 1) continue;
          if (total >= cap) { truncated = true; break; }
          byType.get(t)!.push({ ctor: c.name, args: [] });
          depthOf.get(t)!.push(1);
          total++;
          continue;
        }
        // Candidate field values: everything of depth <= d - 1.
        const pools: Value[][] = [];
        const poolsDepth: number[][] = [];
        let ok = true;
        for (const f of c.fields) {
          const vals = byType.get(f)!;
          const deps = depthOf.get(f)!;
          const pv: Value[] = [];
          const pd: number[] = [];
          for (let i = 0; i < vals.length; i++) {
            if (deps[i]! <= d - 1) { pv.push(vals[i]!); pd.push(deps[i]!); }
          }
          if (pv.length === 0) { ok = false; break; }
          pools.push(pv);
          poolsDepth.push(pd);
        }
        if (!ok) continue;
        // Cartesian product; keep only combos whose max field depth is d - 1
        // so each value is generated exactly once, at its exact depth.
        const idx = new Array<number>(pools.length).fill(0);
        for (;;) {
          let maxField = 0;
          for (let i = 0; i < idx.length; i++) {
            maxField = Math.max(maxField, poolsDepth[i]![idx[i]!]!);
          }
          if (maxField === d - 1) {
            if (total >= cap) { truncated = true; break; }
            const args = idx.map((k, i) => pools[i]![k]!);
            byType.get(t)!.push({ ctor: c.name, args });
            depthOf.get(t)!.push(d);
            total++;
          }
          let pos = idx.length - 1;
          while (pos >= 0 && ++idx[pos]! === pools[pos]!.length) {
            idx[pos] = 0;
            pos--;
          }
          if (pos < 0) break;
        }
        if (truncated) break;
      }
      if (truncated) break;
    }
  }
  return { byType, depthOf, truncated, total };
}
