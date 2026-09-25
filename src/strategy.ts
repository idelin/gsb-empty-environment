import type { PathSeg } from "./json.ts";

export type StrategyName = "override" | "deep" | "byId";

export interface StrategyRule {
  /** Path pattern, e.g. "/services", "/features/*", "/tenants/**". `*` = one segment, `**` = rest. */
  pattern: string;
  strategy: StrategyName;
}

interface ParsedRule {
  segs: string[];
  strategy: StrategyName;
  specificity: number;
}

function parsePattern(pattern: string): string[] {
  if (pattern === "" || pattern === "/") return [];
  if (!pattern.startsWith("/")) throw new Error(`strategy pattern must start with "/": ${pattern}`);
  return pattern.slice(1).split("/");
}

function matchSegs(segs: string[], path: readonly PathSeg[]): boolean {
  let i = 0;
  let j = 0;
  while (i < segs.length) {
    const s = segs[i];
    if (s === "**") return true;
    if (j >= path.length) return false;
    if (s !== "*" && s !== String(path[j])) return false;
    i++;
    j++;
  }
  return j === path.length;
}

export class StrategyMap {
  private readonly rules: ParsedRule[];

  constructor(rules: StrategyRule[] = []) {
    this.rules = rules.map((r) => {
      const segs = parsePattern(r.pattern);
      const literals = segs.filter((s) => s !== "*" && s !== "**").length;
      return { segs, strategy: r.strategy, specificity: literals * 100 + segs.length };
    });
  }

  /** Most specific matching rule wins; default strategy is "deep". */
  strategyAt(path: readonly PathSeg[]): StrategyName {
    let best: ParsedRule | undefined;
    for (const r of this.rules) {
      if (matchSegs(r.segs, path) && (best === undefined || r.specificity > best.specificity)) best = r;
    }
    return best ? best.strategy : "deep";
  }
}
