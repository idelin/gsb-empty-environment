import {
  Decision,
  Effect,
  GroupDef,
  PolicyConfig,
  Rule,
  UpdateResult,
} from './types';
import { normalizePrefix, prefixDepth, prefixMatches } from './paths';
import { validateConfig } from './validate';

interface EngineState {
  groups: Record<string, GroupDef>;
  rules: Rule[];
  /** user id -> set of group ids the user belongs to (transitively). */
  userGroups: Map<string, Set<string>>;
  /** subject id -> rules with that subject (normalized prefixes). */
  rulesBySubject: Map<string, Rule[]>;
}

function normalizeRule(rule: Rule): Rule {
  const prefix = normalizePrefix(rule.prefix);
  return prefix === rule.prefix ? rule : { ...rule, prefix: prefix ?? rule.prefix };
}

/**
 * Computes, for every group, its full transitive member set by memoized DFS
 * over the include graph (guaranteed acyclic by validateConfig), then
 * inverts it into user -> groups.
 */
function computeUserGroups(groups: Record<string, GroupDef>): Map<string, Set<string>> {
  const membersOf = new Map<string, Set<string>>();

  const expand = (gid: string): Set<string> => {
    const cached = membersOf.get(gid);
    if (cached) return cached;
    const acc = new Set<string>();
    membersOf.set(gid, acc); // set before recursion; graph is acyclic
    const def = groups[gid];
    if (def) {
      for (const user of def.members ?? []) acc.add(user);
      for (const child of def.groups ?? []) {
        const childMembers = groups[child] ? expand(child) : undefined;
        if (childMembers) for (const user of childMembers) acc.add(user);
      }
    }
    return acc;
  };

  for (const gid of Object.keys(groups)) expand(gid);

  const userGroups = new Map<string, Set<string>>();
  for (const [gid, members] of membersOf) {
    for (const user of members) {
      let set = userGroups.get(user);
      if (!set) {
        set = new Set<string>();
        userGroups.set(user, set);
      }
      set.add(gid);
    }
  }
  return userGroups;
}

function buildState(config: PolicyConfig): EngineState {
  const rules = (config.rules ?? []).map(normalizeRule);
  const rulesBySubject = new Map<string, Rule[]>();
  for (const rule of rules) {
    const key = rule.subjectKind + ':' + rule.subject;
    const list = rulesBySubject.get(key);
    if (list) list.push(rule);
    else rulesBySubject.set(key, [rule]);
  }
  return {
    groups: config.groups ?? {},
    rules,
    userGroups: computeUserGroups(config.groups ?? {}),
    rulesBySubject,
  };
}

/**
 * Orders matched rules by precedence:
 *   1. more specific prefix (greater depth) first;
 *   2. at equal depth, deny before allow;
 *   3. remaining ties broken deterministically by rule id.
 */
export function compareByPrecedence(a: Rule, b: Rule): number {
  const depthDiff = prefixDepth(b.prefix) - prefixDepth(a.prefix);
  if (depthDiff !== 0) return depthDiff;
  if (a.effect !== b.effect) return a.effect === 'deny' ? -1 : 1;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

export function resolveMatched(
  user: string,
  path: string,
  matched: Rule[],
): Decision {
  const ordered = [...matched].sort(compareByPrecedence);
  const effective = ordered.length > 0 ? ordered[0]! : null;
  return {
    user,
    path,
    effect: effective ? effective.effect : 'deny',
    defaultDeny: effective === null,
    effective,
    shadowed: ordered.slice(1),
    matched: ordered,
  };
}

/**
 * Permission decision engine.
 *
 * Semantics:
 *  - rules apply to path prefixes (segment-aware);
 *  - the most specific matching prefix wins;
 *  - at equal specificity, deny wins;
 *  - when nothing matches, the default is deny.
 *
 * `update` validates the whole config and swaps it atomically: on any
 * validation error (including group inheritance cycles) the previous
 * config stays in effect.
 */
export class PolicyEngine {
  private state: EngineState = buildState({ groups: {}, rules: [] });

  update(config: PolicyConfig): UpdateResult {
    const errors = validateConfig(config);
    if (errors.length > 0) return { ok: false, errors };
    this.state = buildState(config); // atomic swap: no partial mutation
    return { ok: true };
  }

  /** Groups the user belongs to, directly or through inheritance. */
  groupsOf(user: string): string[] {
    return [...(this.state.userGroups.get(user) ?? [])].sort();
  }

  matchedRules(user: string, rawPath: string): Rule[] {
    const path = normalizePrefix(rawPath);
    if (path === null) return [];
    const candidates: Rule[] = [];
    const push = (key: string): void => {
      for (const rule of this.state.rulesBySubject.get(key) ?? []) {
        if (prefixMatches(rule.prefix, path)) candidates.push(rule);
      }
    };
    push('user:' + user);
    for (const gid of this.state.userGroups.get(user) ?? []) push('group:' + gid);
    return candidates;
  }

  decide(user: string, rawPath: string): Decision {
    const path = normalizePrefix(rawPath) ?? rawPath;
    return resolveMatched(user, path, this.matchedRules(user, path));
  }
}
