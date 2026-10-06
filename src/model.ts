/**
 * Reference model: expand every group into a flat rule list, then decide by
 * naive prefix-match scan. Deliberately independent from the trie-based
 * engine so tests can cross-check the two implementations.
 */
import type { Effect, PolicyConfig, Rule, RuleRef, ShadowedEntry } from './types.ts';
import { depth, matchesPrefix, normalizePath } from './normalize.ts';

export class ModelConfigError extends Error {
  readonly cycle: string[] | null;
  constructor(message: string, cycle: string[] | null = null) {
    super(message);
    this.cycle = cycle;
  }
}

/** Recursively expand one group; throws ModelConfigError on cycles/unknown refs. */
function expandOne(
  config: PolicyConfig,
  name: string,
  trail: string[],
  seen: Set<string>,
  out: RuleRef[],
): void {
  const g = config.groups[name];
  if (!g) throw new ModelConfigError(`unknown group "${name}"`);
  if (trail.includes(name)) {
    throw new ModelConfigError('inheritance cycle', [...trail, name]);
  }
  const rules = g.rules ?? [];
  for (let i = 0; i < rules.length; i++) {
    const key = `${name}#${i}`;
    if (!seen.has(key)) {
      seen.add(key);
      const rule: Rule = { prefix: normalizePath(rules[i].prefix), effect: rules[i].effect };
      out.push({ origin: name, index: i, rule, depth: depth(rule.prefix), rank: out.length });
    }
  }
  for (const parent of g.inherits ?? []) {
    expandOne(config, parent, [...trail, name], seen, out);
  }
}

/** Expand every group in the config into flat, ranked rule lists. */
export function expandAll(config: PolicyConfig): Map<string, RuleRef[]> {
  const result = new Map<string, RuleRef[]>();
  for (const name of Object.keys(config.groups)) {
    const out: RuleRef[] = [];
    expandOne(config, name, [], new Set(), out);
    result.set(name, out);
  }
  return result;
}

export interface FlatDecision {
  effect: Effect;
  winner: RuleRef | null;
  matched: RuleRef[];
  reason: 'rule' | 'default-deny' | 'unknown-group';
}

/** Naive scan: filter matching rules, take max depth, deny wins ties. */
export function decideFlat(config: PolicyConfig, group: string, path: string): FlatDecision {
  const all = expandAll(config);
  const refs = all.get(group);
  if (!refs) return { effect: 'deny', winner: null, matched: [], reason: 'unknown-group' };
  const matched = refs.filter((r) => matchesPrefix(r.rule.prefix, path));
  if (matched.length === 0) {
    return { effect: 'deny', winner: null, matched: [], reason: 'default-deny' };
  }
  let best = matched[0];
  for (const r of matched) {
    if (r.depth > best.depth) best = r;
    else if (r.depth === best.depth) {
      if (best.rule.effect === 'allow' && r.rule.effect === 'deny') best = r;
      else if (best.rule.effect === r.rule.effect && r.rank < best.rank) best = r;
    }
  }
  return { effect: best.rule.effect, winner: best, matched, reason: 'rule' };
}

/** Shadowed rules per group, computed straight from the flat expansion. */
export function shadowedFlat(config: PolicyConfig): Map<string, ShadowedEntry[]> {
  const all = expandAll(config);
  const result = new Map<string, ShadowedEntry[]>();
  for (const [name, refs] of all) {
    const entries: ShadowedEntry[] = [];
    for (const r of refs) {
      const samePrefix = refs.filter((o) => o !== r && o.rule.prefix === r.rule.prefix);
      if (samePrefix.length === 0) continue;
      const denies = samePrefix.filter((o) => o.rule.effect === 'deny');
      if (r.rule.effect === 'allow' && denies.length > 0) {
        entries.push({ shadowed: r, shadowedBy: denies[0], reason: 'deny-wins-same-prefix' });
        continue;
      }
      const sameEffect = refs.filter(
        (o) => o.rule.prefix === r.rule.prefix && o.rule.effect === r.rule.effect,
      );
      const first = sameEffect.reduce((a, b) => (a.rank <= b.rank ? a : b));
      if (first !== r) {
        entries.push({ shadowed: r, shadowedBy: first, reason: 'duplicate' });
      }
    }
    result.set(name, entries);
  }
  return result;
}
