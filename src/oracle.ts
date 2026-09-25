import { evalCond } from './constraints.ts';
import type { Input, Policy, RuleVersions } from './model.ts';

export interface VerdictPair {
  a: boolean;
  b: boolean;
}

/** Deterministic key: object keys sorted recursively. */
export function stableStringify(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(stableStringify).join(',')}]`;
  if (value !== null && typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>)
      .sort(([x], [y]) => (x < y ? -1 : 1))
      .map(([k, v]) => `${JSON.stringify(k)}:${stableStringify(v)}`);
    return `{${entries.join(',')}}`;
  }
  return JSON.stringify(value) as string;
}

/**
 * Oracle: judges an input under two rule versions and reports both verdicts.
 * Every uncached evaluation spends one unit of budget; results are cached so
 * repeated probes are free, and the cache is serializable for recovery.
 */
export class Oracle {
  spent = 0;
  private cache = new Map<string, VerdictPair>();

  private readonly policyA: Policy;
  private readonly policyB: Policy;

  constructor(policyA: Policy, policyB: Policy) {
    this.policyA = policyA;
    this.policyB = policyB;
  }

  get ruleVersions(): RuleVersions {
    return { a: this.policyA.version, b: this.policyB.version };
  }

  evaluate(input: Input): VerdictPair {
    const key = stableStringify(input);
    const hit = this.cache.get(key);
    if (hit) return hit;
    this.spent += 1;
    const pair: VerdictPair = {
      a: evalCond(this.policyA.cond, input),
      b: evalCond(this.policyB.cond, input),
    };
    this.cache.set(key, pair);
    return pair;
  }

  /** Cache lookup without spending budget. */
  cached(input: Input): VerdictPair | undefined {
    return this.cache.get(stableStringify(input));
  }

  differs(pair: VerdictPair): boolean {
    return pair.a !== pair.b;
  }

  exportCache(): Record<string, VerdictPair> {
    return Object.fromEntries(this.cache);
  }

  importCache(entries: Record<string, VerdictPair>): void {
    for (const [k, v] of Object.entries(entries)) this.cache.set(k, v);
  }
}
