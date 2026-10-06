import { Decision, PolicyConfig, Rule, UpdateResult } from './types';
import { normalizePrefix, prefixDepth, prefixMatches } from './paths';
import { validateConfig } from './validate';

/**
 * Reference model: deliberately naive, used to cross-check PolicyEngine.
 *
 *  - Group inheritance is resolved by expanding every group to its full
 *    member set with an iterate-to-fixpoint loop (no memoized DFS).
 *  - Decisions scan every rule and select the winner by a direct
 *    "most specific, deny wins ties" comparison instead of sorting.
 */
export class ReferenceModel {
  private config: PolicyConfig = { groups: {}, rules: [] };
  /** group id -> fully expanded member set. */
  private expanded = new Map<string, Set<string>>();

  update(config: PolicyConfig): UpdateResult {
    const errors = validateConfig(config);
    if (errors.length > 0) return { ok: false, errors };
    this.config = config;
    this.expanded = expandAllGroups(config);
    return { ok: true };
  }

  decide(user: string, rawPath: string): Decision {
    const path = normalizePrefix(rawPath) ?? rawPath;
    const matched: Rule[] = [];
    for (const rule of this.config.rules) {
      const prefix = normalizePrefix(rule.prefix);
      if (prefix === null || !prefixMatches(prefix, path)) continue;
      if (rule.subjectKind === 'user') {
        if (rule.subject === user) matched.push({ ...rule, prefix });
      } else {
        const members = this.expanded.get(rule.subject);
        if (members && members.has(user)) matched.push({ ...rule, prefix });
      }
    }

    let winner: Rule | null = null;
    for (const rule of matched) {
      if (winner === null || beats(rule, winner)) winner = rule;
    }
    const shadowed = matched
      .filter((rule) => rule !== winner)
      .sort(compareForReport);
    const ordered = (winner ? [winner] : []).concat(shadowed);
    return {
      user,
      path,
      effect: winner ? winner.effect : 'deny',
      defaultDeny: winner === null,
      effective: winner,
      shadowed,
      matched: ordered,
    };
  }
}

/** True when `a` outranks `b`: deeper prefix, or same depth and deny, or id tie-break. */
function beats(a: Rule, b: Rule): boolean {
  const da = prefixDepth(a.prefix);
  const db = prefixDepth(b.prefix);
  if (da !== db) return da > db;
  if (a.effect !== b.effect) return a.effect === 'deny';
  return a.id < b.id;
}

function compareForReport(a: Rule, b: Rule): number {
  if (beats(a, b)) return -1;
  if (beats(b, a)) return 1;
  return 0;
}

/**
 * Expands every group to its transitive member set by iterating a
 * membership union pass until no set grows (config is cycle-free).
 */
export function expandAllGroups(config: PolicyConfig): Map<string, Set<string>> {
  const groups = config.groups ?? {};
  const expanded = new Map<string, Set<string>>();
  for (const [gid, def] of Object.entries(groups)) {
    expanded.set(gid, new Set(def.members ?? []));
  }
  let changed = true;
  while (changed) {
    changed = false;
    for (const [gid, def] of Object.entries(groups)) {
      const acc = expanded.get(gid)!;
      for (const child of def.groups ?? []) {
        const childMembers = expanded.get(child);
        if (!childMembers) continue;
        for (const user of childMembers) {
          if (!acc.has(user)) {
            acc.add(user);
            changed = true;
          }
        }
      }
    }
  }
  return expanded;
}
