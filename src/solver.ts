import { resolveRoles } from './inheritance.ts';
import {
  MAX_ROLE_CATALOG_SIZE,
  type GrantRequest,
  type GrantResult,
  type PermissionWitness,
} from './types.ts';

function popcount(value: bigint): number {
  let count = 0;
  let bits = value;
  while (bits !== 0n) {
    bits &= bits - 1n;
    count += 1;
  }
  return count;
}

interface Candidate {
  name: string;
  mask: bigint;
  requiredGain: number;
}

/**
 * Solves the role grant problem exactly (branch and bound over all
 * combinations, never a greedy approximation):
 *   1. minimize the number of granted roles;
 *   2. among those, minimize excess (non-required) permissions granted.
 * Roles carrying any sensitive permission are never granted.
 */
export function solveGrant(request: GrantRequest): GrantResult {
  const roles = request.roles ?? [];
  const required = [...new Set(request.required ?? [])];
  const sensitive = new Set(request.sensitive ?? []);
  const maxRoles = request.maxRoles ?? MAX_ROLE_CATALOG_SIZE;

  const errors: string[] = [];
  if (roles.length > MAX_ROLE_CATALOG_SIZE) {
    errors.push(
      `role catalog has ${roles.length} roles; at most ${MAX_ROLE_CATALOG_SIZE} are supported`,
    );
  }
  if (!Number.isInteger(maxRoles) || maxRoles < 0) {
    errors.push(`maxRoles must be a non-negative integer, got ${maxRoles}`);
  }
  if (errors.length > 0) return { ok: false, reason: 'invalid', errors };

  const resolved = resolveRoles(roles);
  if (resolved.errors.length > 0) {
    return { ok: false, reason: 'invalid', errors: resolved.errors };
  }
  const warnings = resolved.warnings;

  const permissionIndex = new Map<string, number>();
  const indexOf = (permission: string): number => {
    let index = permissionIndex.get(permission);
    if (index === undefined) {
      index = permissionIndex.size;
      permissionIndex.set(permission, index);
    }
    return index;
  };

  let requiredMask = 0n;
  for (const permission of required) requiredMask |= 1n << BigInt(indexOf(permission));

  let sensitiveMask = 0n;
  for (const permission of sensitive) sensitiveMask |= 1n << BigInt(indexOf(permission));

  const roleMasks = new Map<string, bigint>();
  for (const role of roles) {
    let mask = 0n;
    for (const permission of resolved.effective.get(role.name)!) {
      mask |= 1n << BigInt(indexOf(permission));
    }
    roleMasks.set(role.name, mask);
  }

  const witnesses = collectWitnesses(
    roles,
    required,
    sensitive,
    resolved.effective,
  );
  if (witnesses.length > 0) {
    return { ok: false, reason: 'uncoverable', witnesses, warnings };
  }

  if (requiredMask === 0n) {
    return {
      ok: true,
      roles: [],
      roleCount: 0,
      grantedPermissions: [],
      excessPermissions: [],
      warnings,
    };
  }

  const candidates: Candidate[] = [];
  const seenMasks = new Set<bigint>();
  for (const role of roles) {
    const mask = roleMasks.get(role.name)!;
    if ((mask & sensitiveMask) !== 0n) continue;
    if ((mask & requiredMask) === 0n) continue;
    if (seenMasks.has(mask)) continue;
    seenMasks.add(mask);
    candidates.push({
      name: role.name,
      mask,
      requiredGain: popcount(mask & requiredMask),
    });
  }
  candidates.sort((a, b) => b.requiredGain - a.requiredGain || a.name.localeCompare(b.name));

  const suffixUnion = new Array<bigint>(candidates.length + 1).fill(0n);
  for (let i = candidates.length - 1; i >= 0; i -= 1) {
    suffixUnion[i] = suffixUnion[i + 1] | candidates[i].mask;
  }

  let bestCount = Number.POSITIVE_INFINITY;
  let bestExcess = Number.POSITIVE_INFINITY;
  let bestCombo: string[] = [];
  const chosen: string[] = [];

  const search = (position: number, covered: bigint): void => {
    const remaining = requiredMask & ~covered;
    if (remaining === 0n) {
      const excess = popcount(covered & ~requiredMask);
      if (chosen.length < bestCount || (chosen.length === bestCount && excess < bestExcess)) {
        bestCount = chosen.length;
        bestExcess = excess;
        bestCombo = [...chosen];
      }
      return;
    }
    if (position >= candidates.length) return;
    if (chosen.length >= bestCount) return;
    if ((suffixUnion[position] & remaining) !== remaining) return;

    let maxGain = 0;
    for (let i = position; i < candidates.length; i += 1) {
      const gain = popcount(candidates[i].mask & remaining);
      if (gain > maxGain) maxGain = gain;
    }
    if (chosen.length + Math.ceil(popcount(remaining) / maxGain) > bestCount) return;

    const currentExcess = popcount(covered & ~requiredMask);
    if (chosen.length + 1 >= bestCount && currentExcess >= bestExcess) return;

    chosen.push(candidates[position].name);
    search(position + 1, covered | candidates[position].mask);
    chosen.pop();
    search(position + 1, covered);
  };
  search(0, 0n);

  const grantedMask = bestCombo.reduce(
    (mask, name) => mask | roleMasks.get(name)!,
    0n,
  );
  const toNames = (mask: bigint): string[] => {
    const names: string[] = [];
    for (const [permission, index] of permissionIndex) {
      if ((mask & (1n << BigInt(index))) !== 0n) names.push(permission);
    }
    return names.sort();
  };

  const sortedCombo = [...bestCombo].sort();
  if (bestCount > maxRoles) {
    return {
      ok: false,
      reason: 'role-limit-exceeded',
      maxRoles,
      minimumRolesNeeded: bestCount,
      optimalCombination: sortedCombo,
      warnings,
    };
  }

  return {
    ok: true,
    roles: sortedCombo,
    roleCount: bestCount,
    grantedPermissions: toNames(grantedMask),
    excessPermissions: toNames(grantedMask & ~requiredMask),
    warnings,
  };
}

function collectWitnesses(
  roles: readonly { name: string }[],
  required: readonly string[],
  sensitive: ReadonlySet<string>,
  effective: ReadonlyMap<string, Set<string>>,
): PermissionWitness[] {
  const witnesses: PermissionWitness[] = [];
  for (const permission of [...required].sort()) {
    if (sensitive.has(permission)) {
      witnesses.push({ kind: 'sensitive-conflict', permission });
      continue;
    }
    const grantors = roles.filter((role) =>
      effective.get(role.name)!.has(permission),
    );
    if (grantors.length === 0) {
      witnesses.push({ kind: 'missing', permission });
      continue;
    }
    const blockedBy = grantors
      .map((role) => ({
        role: role.name,
        sensitivePermissions: [...effective.get(role.name)!]
          .filter((p) => sensitive.has(p))
          .sort(),
      }))
      .filter((entry) => entry.sensitivePermissions.length > 0);
    if (blockedBy.length === grantors.length) {
      blockedBy.sort((a, b) => a.role.localeCompare(b.role));
      witnesses.push({ kind: 'forced-sensitive', permission, blockedBy });
    }
  }
  return witnesses;
}
