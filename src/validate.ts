import { GroupDef, PolicyConfig, ValidationError } from './types';
import { normalizePrefix } from './paths';

/**
 * Detects one inheritance cycle in the group graph, if any.
 * Edge A -> B means "A includes B". Returns the cycle as a list of
 * group ids (first element repeated at the end), or null when acyclic.
 */
export function findInheritanceCycle(groups: Record<string, GroupDef>): string[] | null {
  const WHITE = 0;
  const GRAY = 1;
  const BLACK = 2;
  const color = new Map<string, number>();
  const path: string[] = [];

  const dfs = (node: string): string[] | null => {
    color.set(node, GRAY);
    path.push(node);
    const def = groups[node];
    for (const next of (def && def.groups) || []) {
      if (!(next in groups)) continue; // unknown refs reported separately
      const c = color.get(next) ?? WHITE;
      if (c === GRAY) {
        const start = path.indexOf(next);
        return path.slice(start).concat(next);
      }
      if (c === WHITE) {
        const found = dfs(next);
        if (found) return found;
      }
    }
    path.pop();
    color.set(node, BLACK);
    return null;
  };

  for (const id of Object.keys(groups)) {
    if ((color.get(id) ?? WHITE) === WHITE) {
      const found = dfs(id);
      if (found) return found;
    }
  }
  return null;
}

/**
 * Validates a config. The update is rejected (and the previous config kept)
 * when this returns a non-empty list.
 */
export function validateConfig(config: PolicyConfig): ValidationError[] {
  const errors: ValidationError[] = [];
  const groups = config.groups ?? {};
  const rules = config.rules ?? [];

  const seenIds = new Set<string>();
  for (const rule of rules) {
    if (seenIds.has(rule.id)) {
      errors.push({
        kind: 'duplicate-rule-id',
        message: `duplicate rule id "${rule.id}"`,
      });
    }
    seenIds.add(rule.id);

    if (normalizePrefix(rule.prefix) === null) {
      errors.push({
        kind: 'invalid-prefix',
        message: `rule "${rule.id}" has an empty or invalid prefix`,
      });
    }
    if (rule.effect !== 'allow' && rule.effect !== 'deny') {
      errors.push({
        kind: 'invalid-effect',
        message: `rule "${rule.id}" has invalid effect "${String(rule.effect)}"`,
      });
    }
    if (rule.subjectKind === 'group' && !(rule.subject in groups)) {
      errors.push({
        kind: 'unknown-group',
        message: `rule "${rule.id}" targets unknown group "${rule.subject}"`,
      });
    }
  }

  for (const [gid, def] of Object.entries(groups)) {
    for (const included of (def && def.groups) || []) {
      if (!(included in groups)) {
        errors.push({
          kind: 'unknown-group',
          message: `group "${gid}" includes unknown group "${included}"`,
        });
      }
    }
  }

  const cycle = findInheritanceCycle(groups);
  if (cycle) {
    errors.push({
      kind: 'inheritance-cycle',
      message: `group inheritance cycle detected: ${cycle.join(' -> ')}`,
      cycle,
    });
  }

  return errors;
}
