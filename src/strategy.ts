import type { Path } from "./json.ts";

/** Per-field merge strategy. */
export type MergeStrategy =
  | { readonly kind: "override" }
  | { readonly kind: "deep" }
  | { readonly kind: "byId"; readonly idKey: string };

export const OVERRIDE: MergeStrategy = { kind: "override" };
export const DEEP: MergeStrategy = { kind: "deep" };
export function byId(idKey: string): MergeStrategy {
  return { kind: "byId", idKey };
}

/**
 * A rule binds a strategy to an exact field path; `*` matches one segment.
 * Example: { pattern: ["plugins"], strategy: byId("name") }.
 */
export interface StrategyRule {
  readonly pattern: readonly string[];
  readonly strategy: MergeStrategy;
}

/** Most-specific (longest) matching pattern wins; default strategy is deep. */
export class StrategyMap {
  private readonly rules: readonly StrategyRule[];

  constructor(rules: readonly StrategyRule[] = []) {
    this.rules = [...rules];
  }

  strategyAt(path: Path): MergeStrategy {
    let best: MergeStrategy = DEEP;
    let bestLen = -1;
    for (const rule of this.rules) {
      if (rule.pattern.length !== path.length) continue;
      if (rule.pattern.length <= bestLen) continue;
      const match = rule.pattern.every((p, i) => p === "*" || p === path[i]);
      if (match) {
        best = rule.strategy;
        bestLen = rule.pattern.length;
      }
    }
    return best;
  }
}
