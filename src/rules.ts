import type { ApprovalRule } from './types.js';
import { matchPath } from './glob.js';

export interface PathRequirement {
  owners: string[];
  minApprovals: number;
}

export function resolveRequirement(
  rules : ApprovalRule[],
  path : string,
  defaultMinApprovals : number,
) : PathRequirement | null {
  let owners = new Set<string>();
  let threshold = defaultMinApprovals;
  let matched = false;
  for (const rule of rules) {
    if (!matchPath(rule.pattern, path)) continue;
    matched = true;
    if (rule.mode === 'override') {
      owners = new Set<string>(rule.owners);
    } else {
      for (const owner of rule.owners) owners.add(owner);
    }
    if (rule.minApprovals !== undefined) threshold = rule.minApprovals;
  }
  if (!matched) return null;
  return { owners: Array.from(owners), minApprovals: threshold };
}
