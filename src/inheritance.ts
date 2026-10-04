import type { RoleDefinition } from './types.ts';

export interface ResolvedRoles {
  effective: Map<string, Set<string>>;
  warnings: string[];
  errors: string[];
}

/**
 * Computes effective (transitively inherited) permissions for every role.
 * Uses a fixpoint so inheritance cycles resolve to the union of all
 * permissions on the cycle; cycles are reported as warnings.
 */
export function resolveRoles(roles: readonly RoleDefinition[]): ResolvedRoles {
  const errors: string[] = [];
  const warnings: string[] = [];
  const effective = new Map<string, Set<string>>();
  const inherits = new Map<string, readonly string[]>();

  for (const role of roles) {
    if (effective.has(role.name)) {
      errors.push(`duplicate role name: "${role.name}"`);
      continue;
    }
    effective.set(role.name, new Set(role.permissions));
    inherits.set(role.name, role.inherits ?? []);
  }
  if (errors.length > 0) return { effective, warnings, errors };

  for (const role of roles) {
    for (const parent of inherits.get(role.name)!) {
      if (!effective.has(parent)) {
        errors.push(`role "${role.name}" inherits unknown role "${parent}"`);
      }
    }
  }
  if (errors.length > 0) return { effective, warnings, errors };

  let changed = true;
  while (changed) {
    changed = false;
    for (const role of roles) {
      const target = effective.get(role.name)!;
      for (const parent of inherits.get(role.name)!) {
        for (const permission of effective.get(parent)!) {
          if (!target.has(permission)) {
            target.add(permission);
            changed = true;
          }
        }
      }
    }
  }

  for (const cycle of findCycles(roles, inherits)) {
    warnings.push(`inheritance cycle detected: ${cycle.join(' -> ')}`);
  }
  return { effective, warnings, errors };
}

function findCycles(
  roles: readonly RoleDefinition[],
  inherits: Map<string, readonly string[]>,
): string[][] {
  const cycles: string[][] = [];
  const state = new Map<string, 'visiting' | 'done'>();
  const stack: string[] = [];

  const visit = (name: string): void => {
    const current = state.get(name);
    if (current === 'done') return;
    if (current === 'visiting') {
      const start = stack.indexOf(name);
      cycles.push([...stack.slice(start), name]);
      return;
    }
    state.set(name, 'visiting');
    stack.push(name);
    for (const parent of inherits.get(name) ?? []) visit(parent);
    stack.pop();
    state.set(name, 'done');
  };

  for (const role of roles) visit(role.name);
  return cycles;
}
