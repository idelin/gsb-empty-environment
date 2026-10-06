import type {
  Decision,
  Effect,
  GroupExplanation,
  PolicyConfig,
  Rule,
  RuleRef,
  ShadowedEntry,
  UpdateError,
  UpdateResult,
} from './types.ts';
import { depth, matchesPrefix, normalizePath, segments } from './normalize.ts';

interface NormalizedGroup {
  rules: Rule[];
  inherits: string[];
}

interface State {
  config: PolicyConfig;
  groups: Map<string, NormalizedGroup>;
  /** Expanded, ranked, deduped rules per group. */
  expanded: Map<string, RuleRef[]>;
  /** Prefix trie per group for decision lookups. */
  tries: Map<string, TrieNode>;
  explanations: Map<string, GroupExplanation>;
}

interface TrieNode {
  rules: RuleRef[];
  children: Map<string, TrieNode>;
}

function newNode(): TrieNode {
  return { rules: [], children: new Map() };
}

function validateAndNormalize(config: PolicyConfig):
  | { ok: true; groups: Map<string, NormalizedGroup> }
  | { ok: false; error: UpdateError } {
  if (config === null || typeof config !== 'object' || Array.isArray(config)) {
    return { ok: false, error: { kind: 'invalid-config', message: 'config must be an object' } };
  }
  const rawGroups = (config as PolicyConfig).groups;
  if (rawGroups === null || typeof rawGroups !== 'object' || Array.isArray(rawGroups)) {
    return { ok: false, error: { kind: 'invalid-config', message: 'config.groups must be an object' } };
  }
  const groups = new Map<string, NormalizedGroup>();
  for (const [name, g] of Object.entries(rawGroups)) {
    if (g === null || typeof g !== 'object' || Array.isArray(g)) {
      return { ok: false, error: { kind: 'invalid-config', message: `group "${name}" must be an object` } };
    }
    const rulesIn = g.rules ?? [];
    const inheritsIn = g.inherits ?? [];
    if (!Array.isArray(rulesIn) || !Array.isArray(inheritsIn)) {
      return { ok: false, error: { kind: 'invalid-config', message: `group "${name}": rules and inherits must be arrays` } };
    }
    const rules: Rule[] = [];
    for (let i = 0; i < rulesIn.length; i++) {
      const r = rulesIn[i];
      if (r === null || typeof r !== 'object' || typeof r.prefix !== 'string' ||
          (r.effect !== 'allow' && r.effect !== 'deny')) {
        return { ok: false, error: { kind: 'invalid-config', message: `group "${name}" rule #${i} is invalid` } };
      }
      rules.push({ prefix: normalizePath(r.prefix), effect: r.effect });
    }
    const inherits: string[] = [];
    for (const parent of inheritsIn) {
      if (typeof parent !== 'string') {
        return { ok: false, error: { kind: 'invalid-config', message: `group "${name}": inherits entries must be strings` } };
      }
      if (!(parent in rawGroups)) {
        return { ok: false, error: { kind: 'unknown-group', group: parent, referencedBy: name } };
      }
      if (!inherits.includes(parent)) inherits.push(parent);
    }
    groups.set(name, { rules, inherits });
  }
  const cycle = findCycle(groups);
  if (cycle) {
    return { ok: false, error: { kind: 'inheritance-cycle', cycle } };
  }
  return { ok: true, groups };
}

/** Iterative DFS over the inheritance graph; returns the cycle path if any. */
function findCycle(groups: Map<string, NormalizedGroup>): string[] | null {
  const WHITE = 0, GRAY = 1, BLACK = 2;
  const color = new Map<string, number>();
  for (const name of groups.keys()) color.set(name, WHITE);
  for (const start of groups.keys()) {
    if (color.get(start) !== WHITE) continue;
    const stack: { name: string; next: number }[] = [{ name: start, next: 0 }];
    color.set(start, GRAY);
    while (stack.length > 0) {
      const top = stack[stack.length - 1];
      const parents = groups.get(top.name)!.inherits;
      if (top.next < parents.length) {
        const parent = parents[top.next++];
        const c = color.get(parent)!;
        if (c === GRAY) {
          const idx = stack.findIndex((f) => f.name === parent);
          return stack.slice(idx).map((f) => f.name).concat(parent);
        }
        if (c === WHITE) {
          color.set(parent, GRAY);
          stack.push({ name: parent, next: 0 });
        }
      } else {
        color.set(top.name, BLACK);
        stack.pop();
      }
    }
  }
  return null;
}

/** Expand one group: own rules first, then inherited rules in DFS order.
 *  Dedupes by rule origin so diamond inheritance yields each rule once. */
function expandGroup(
  name: string,
  groups: Map<string, NormalizedGroup>,
  memo: Map<string, RuleRef[]>,
): RuleRef[] {
  const cached = memo.get(name);
  if (cached) return cached;
  const out: RuleRef[] = [];
  const seen = new Set<string>();
  const push = (origin: string, index: number, rule: Rule) => {
    const key = `${origin}#${index}`;
    if (seen.has(key)) return;
    seen.add(key);
    out.push({
      origin,
      index,
      rule,
      depth: depth(rule.prefix),
      rank: out.length,
    });
  };
  const g = groups.get(name)!;
  g.rules.forEach((rule, index) => push(name, index, rule));
  for (const parent of g.inherits) {
    for (const ref of expandGroup(parent, groups, memo)) {
      push(ref.origin, ref.index, ref.rule);
    }
  }
  memo.set(name, out);
  return out;
}

function buildTrie(refs: RuleRef[]): TrieNode {
  const root = newNode();
  for (const ref of refs) {
    let node = root;
    for (const seg of segments(ref.rule.prefix)) {
      let child = node.children.get(seg);
      if (!child) {
        child = newNode();
        node.children.set(seg, child);
      }
      node = child;
    }
    node.rules.push(ref);
  }
  return root;
}

/** Rules that can never win: same-prefix deny beats allow; exact duplicates
 *  collapse to the lowest-rank (closest to the querying group) copy. */
function computeShadowed(refs: RuleRef[]): { effective: RuleRef[]; shadowed: ShadowedEntry[] } {
  const byPrefix = new Map<string, RuleRef[]>();
  for (const ref of refs) {
    const bucket = byPrefix.get(ref.rule.prefix);
    if (bucket) bucket.push(ref);
    else byPrefix.set(ref.rule.prefix, [ref]);
  }
  const shadowed: ShadowedEntry[] = [];
  const dead = new Set<RuleRef>();
  for (const bucket of byPrefix.values()) {
    if (bucket.length < 2) continue;
    const denies = bucket.filter((r) => r.rule.effect === 'deny');
    const allows = bucket.filter((r) => r.rule.effect === 'allow');
    if (denies.length > 0) {
      const winner = denies[0];
      for (const a of allows) {
        shadowed.push({ shadowed: a, shadowedBy: winner, reason: 'deny-wins-same-prefix' });
        dead.add(a);
      }
      for (const d of denies.slice(1)) {
        shadowed.push({ shadowed: d, shadowedBy: winner, reason: 'duplicate' });
        dead.add(d);
      }
    } else {
      const winner = allows[0];
      for (const a of allows.slice(1)) {
        shadowed.push({ shadowed: a, shadowedBy: winner, reason: 'duplicate' });
        dead.add(a);
      }
    }
  }
  return { effective: refs.filter((r) => !dead.has(r)), shadowed };
}

function pickWinner(candidates: RuleRef[]): RuleRef {
  let best = candidates[0];
  for (const c of candidates) {
    if (c.depth > best.depth) best = c;
    else if (c.depth === best.depth) {
      // Same depth + both match the same path => identical prefix.
      if (best.rule.effect === 'allow' && c.rule.effect === 'deny') best = c;
      else if (best.rule.effect === c.rule.effect && c.rank < best.rank) best = c;
    }
  }
  return best;
}

function sortMatched(refs: RuleRef[]): RuleRef[] {
  const effectRank = (e: Effect) => (e === 'deny' ? 0 : 1);
  return [...refs].sort((a, b) =>
    b.depth - a.depth ||
    effectRank(a.rule.effect) - effectRank(b.rule.effect) ||
    a.rank - b.rank,
  );
}

export class PolicyEngine {
  private state: State;

  constructor(config: PolicyConfig = { groups: {} }) {
    const result = buildState(config);
    if (!result.ok) {
      const err = result.error;
      throw new Error(`invalid initial config: ${formatError(err)}`);
    }
    this.state = result.state;
  }

  /** Atomically replace the whole config. On validation failure nothing changes. */
  update(config: PolicyConfig): UpdateResult {
    const result = buildState(config);
    if (!result.ok) return { ok: false, error: result.error };
    this.state = result.state;
    return { ok: true };
  }

  /** Current config as accepted (normalized). */
  snapshot(): PolicyConfig {
    return structuredClone(this.state.config);
  }

  decide(group: string, path: string): Decision {
    const normalized = normalizePath(path);
    const trie = this.state.tries.get(group);
    if (!trie) {
      return { group, path: normalized, effect: 'deny', winner: null, matched: [], reason: 'unknown-group' };
    }
    const candidates: RuleRef[] = [...trie.rules];
    let node = trie;
    for (const seg of segments(normalized)) {
      const child = node.children.get(seg);
      if (!child) break;
      node = child;
      candidates.push(...node.rules);
    }
    if (candidates.length === 0) {
      return { group, path: normalized, effect: 'deny', winner: null, matched: [], reason: 'default-deny' };
    }
    const winner = pickWinner(candidates);
    return {
      group,
      path: normalized,
      effect: winner.rule.effect,
      winner,
      matched: sortMatched(candidates),
      reason: 'rule',
    };
  }

  /** Actual effective rules and shadowed rules for a group. */
  explain(group: string): GroupExplanation {
    const found = this.state.explanations.get(group);
    if (!found) return { group, effective: [], shadowed: [] };
    return structuredClone(found);
  }
}

function buildState(config: PolicyConfig):
  | { ok: true; state: State }
  | { ok: false; error: UpdateError } {
  const validated = validateAndNormalize(config);
  if (!validated.ok) return validated;
  const groups = validated.groups;
  const expanded = new Map<string, RuleRef[]>();
  for (const name of groups.keys()) expandGroup(name, groups, expanded);
  const tries = new Map<string, TrieNode>();
  const explanations = new Map<string, GroupExplanation>();
  for (const [name, refs] of expanded) {
    tries.set(name, buildTrie(refs));
    const { effective, shadowed } = computeShadowed(refs);
    explanations.set(name, { group: name, effective, shadowed });
  }
  return {
    ok: true,
    state: { config: structuredClone(config), groups, expanded, tries, explanations },
  };
}

function formatError(err: UpdateError): string {
  switch (err.kind) {
    case 'inheritance-cycle':
      return `inheritance cycle: ${err.cycle.join(' -> ')}`;
    case 'unknown-group':
      return `group "${err.referencedBy}" inherits unknown group "${err.group}"`;
    case 'invalid-config':
      return err.message;
  }
}

export { matchesPrefix, normalizePath };
