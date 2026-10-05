/**
 * 角色授予求解器。
 *
 * 从角色权限集合中选出组合，使用户所需权限被完全覆盖，且不得引入任何
 * 指定敏感权限。目标按字典序优化：
 *   1. 角色数量最少；
 *   2. 多余权限（授予但未被要求）最少。
 *
 * 求解采用迭代加深 + 分支限界的精确搜索，保证最优性，不是贪心近似。
 */

export interface Role {
  name: string;
  permissions: readonly string[];
  /** 继承的角色名，继承其全部有效权限（可多级，禁止成环）。 */
  inherits?: readonly string[];
}

export interface GrantRequest {
  roles: readonly Role[];
  /** 用户所需权限。 */
  required: readonly string[];
  /** 禁止授予的敏感权限。 */
  sensitive?: readonly string[];
  /** 组合中允许的最大角色数，默认 25。 */
  maxRoles?: number;
}

export interface GrantSolution {
  status: "ok";
  /** 选中的角色名。 */
  roles: string[];
  /** 组合实际授予的全部有效权限。 */
  granted: string[];
  /** 多余权限：granted 中不属于 required 的部分。 */
  excess: string[];
}

export interface ForcedSensitiveWitness {
  /** 被迫引入敏感权限的所需权限。 */
  permission: string;
  /** 所有能授予该权限的角色都会带入的敏感权限集合。 */
  sensitivePermissions: string[];
  /** 能有效授予该权限的全部角色（均被敏感权限禁用）。 */
  roles: string[];
}

export interface UnsatResult {
  status: "unsat";
  /** 任何角色都无法授予的所需权限。 */
  missingPermissions: string[];
  /** 只能由携带敏感权限的角色授予的所需权限见证。 */
  forcedSensitive: ForcedSensitiveWitness[];
  /** 存在覆盖方案但超出角色数上限时的见证。 */
  maxRolesExceeded?: { maxRoles: number };
}

export type GrantResult = GrantSolution | UnsatResult;

const DEFAULT_MAX_ROLES = 25;

/** 解析继承，返回每个角色的有效权限集合（传递闭包）。 */
export function resolveEffectivePermissions(
  roles: readonly Role[]
): Map<string, Set<string>> {
  const byName = new Map<string, Role>();
  for (const role of roles) {
    if (byName.has(role.name)) {
      throw new Error(`Duplicate role name: ${role.name}`);
    }
    byName.set(role.name, role);
  }

  const memo = new Map<string, Set<string>>();
  const visiting = new Set<string>();

  const resolve = (name: string, stack: string[]): Set<string> => {
    const cached = memo.get(name);
    if (cached) return cached;
    const role = byName.get(name);
    if (!role) {
      throw new Error(
        `Unknown role "${name}" referenced in inherits of "${stack[stack.length - 1]}"`
      );
    }
    if (visiting.has(name)) {
      throw new Error(`Inheritance cycle detected: ${stack.concat(name).join(" -> ")}`);
    }
    visiting.add(name);
    const effective = new Set<string>(role.permissions);
    for (const parent of role.inherits !== undefined ? role.inherits : []) {
      for (const perm of resolve(parent, stack.concat(name))) {
        effective.add(perm);
      }
    }
    visiting.delete(name);
    memo.set(name, effective);
    return effective;
  };

  const result = new Map<string, Set<string>>();
  for (const name of byName.keys()) {
    result.set(name, resolve(name, []));
  }
  return result;
}

interface Candidate {
  name: string;
  effective: string[];
  /** effective 中属于 required 的部分。 */
  requiredPerms: string[];
}

/**
 * 精确求解最优授予组合。
 *
 * 搜索结构：对角色数 k = 1..maxRoles 迭代加深；固定 k 后用分支限界
 * 枚举全部大小不超过 k 的覆盖组合，取多余权限最小者。限界只剪去
 * 被支配的分支（多余权限已不优于当前最优），因此结果精确最优。
 */
export function solveGrant(request: GrantRequest): GrantResult {
  const maxRoles =
    request.maxRoles !== undefined ? request.maxRoles : DEFAULT_MAX_ROLES;
  if (!Number.isInteger(maxRoles) || maxRoles < 0) {
    throw new Error(`maxRoles must be a non-negative integer, got ${maxRoles}`);
  }

  const required = new Set<string>(request.required);
  const sensitive = new Set<string>(
    request.sensitive !== undefined ? request.sensitive : []
  );

  if (required.size === 0) {
    return { status: "ok", roles: [], granted: [], excess: [] };
  }

  const effective = resolveEffectivePermissions(request.roles);

  // 每个角色被哪些敏感权限禁用。
  const blockedBy = new Map<string, Set<string>>();
  for (const [name, eff] of effective) {
    const hits = new Set<string>();
    for (const perm of eff) {
      if (sensitive.has(perm)) hits.add(perm);
    }
    if (hits.size > 0) blockedBy.set(name, hits);
  }

  // 无解见证：缺失权限 与 被迫敏感权限。
  const missingPermissions: string[] = [];
  const forcedSensitive: ForcedSensitiveWitness[] = [];
  for (const perm of required) {
    const granters: string[] = [];
    for (const [name, eff] of effective) {
      if (eff.has(perm)) granters.push(name);
    }
    if (granters.length === 0) {
      missingPermissions.push(perm);
      continue;
    }
    const usable = granters.filter((name) => !blockedBy.has(name));
    if (usable.length === 0) {
      const sens = new Set<string>();
      if (sensitive.has(perm)) sens.add(perm);
      for (const name of granters) {
        for (const s of blockedBy.get(name)!) sens.add(s);
      }
      forcedSensitive.push({
        permission: perm,
        sensitivePermissions: Array.from(sens).sort(),
        roles: granters.slice().sort(),
      });
    }
  }
  if (missingPermissions.length > 0 || forcedSensitive.length > 0) {
    return {
      status: "unsat",
      missingPermissions: missingPermissions.sort(),
      forcedSensitive,
    };
  }

  // 候选角色：未被敏感权限禁用，且至少贡献一个所需权限。
  // （不贡献所需权限的角色只会增加角色数与多余权限，不可能出现在最优解中。）
  // 有效权限集合完全相同的角色可互相替代，仅保留名字序最小者。
  const seenSignature = new Map<string, string>();
  const candidates: Candidate[] = [];
  const sortedNames = Array.from(effective.keys()).sort();
  for (const name of sortedNames) {
    if (blockedBy.has(name)) continue;
    const eff = effective.get(name)!;
    const requiredPerms: string[] = [];
    for (const perm of eff) {
      if (required.has(perm)) requiredPerms.push(perm);
    }
    if (requiredPerms.length === 0) continue;
    const signature = Array.from(eff).sort().join("");
    if (seenSignature.has(signature)) continue;
    seenSignature.set(signature, name);
    candidates.push({
      name,
      effective: Array.from(eff),
      requiredPerms,
    });
  }

  // 迭代加深：先最小化角色数，再在当前 k 下精确最小化多余权限。
  for (let k = 1; k <= maxRoles; k++) {
    const found = searchExact(k, candidates, required);
    if (found !== null) {
      const granted = new Set<string>();
      for (const name of found) {
        for (const perm of effective.get(name)!) granted.add(perm);
      }
      const excess: string[] = [];
      for (const perm of granted) {
        if (!required.has(perm)) excess.push(perm);
      }
      return {
        status: "ok",
        roles: found,
        granted: Array.from(granted).sort(),
        excess: excess.sort(),
      };
    }
  }

  return {
    status: "unsat",
    missingPermissions: [],
    forcedSensitive: [],
    maxRolesExceeded: { maxRoles },
  };
}

/**
 * 在角色数不超过 limit 的组合中，精确搜索覆盖 required 且多余权限
 * 最少的组合；无解返回 null。
 */
function searchExact(
  limit: number,
  candidates: Candidate[],
  required: ReadonlySet<string>
): string[] | null {
  // 后缀并集：suffixCover[i] = candidates[i..] 能覆盖的所需权限集合，
  // 用于可行性剪枝（剩余角色是否还有可能补齐缺口）。
  const n = candidates.length;
  const suffixCover: Set<string>[] = new Array<Set<string>>(n + 1);
  suffixCover[n] = new Set<string>();
  for (let i = n - 1; i >= 0; i--) {
    const cover = new Set<string>(suffixCover[i + 1]);
    for (const perm of candidates[i].requiredPerms) cover.add(perm);
    suffixCover[i] = cover;
  }

  let bestExcess = Infinity;
  let bestRoles: string[] | null = null;

  const covered = new Set<string>();
  const union = new Set<string>();
  const chosen: string[] = [];
  let excessSoFar = 0;

  const feasible = (fromIndex: number): boolean => {
    const reachable = suffixCover[fromIndex];
    for (const perm of required) {
      if (!covered.has(perm) && !reachable.has(perm)) return false;
    }
    return true;
  };

  const dfs = (startIndex: number, slotsLeft: number): void => {
    if (covered.size === required.size) {
      if (excessSoFar < bestExcess) {
        bestExcess = excessSoFar;
        bestRoles = chosen.slice();
      }
      return;
    }
    if (slotsLeft === 0) return;
    if (excessSoFar >= bestExcess) return;
    if (!feasible(startIndex)) return;

    for (let i = startIndex; i < n; i++) {
      const cand = candidates[i];

      let addsRequired = 0;
      for (const perm of cand.requiredPerms) {
        if (!covered.has(perm)) addsRequired++;
      }
      if (addsRequired === 0) continue; // 只增角色数与多余权限，无益

      let addedExcess = 0;
      for (const perm of cand.effective) {
        if (!union.has(perm) && !required.has(perm)) addedExcess++;
      }
      // 多余权限只会继续增加，已不可能严格优于当前最优则剪枝。
      if (excessSoFar + addedExcess >= bestExcess) continue;

      const addedCovered: string[] = [];
      const addedUnion: string[] = [];
      for (const perm of cand.effective) {
        if (!union.has(perm)) {
          union.add(perm);
          addedUnion.push(perm);
          if (!required.has(perm)) excessSoFar++;
        }
        if (required.has(perm) && !covered.has(perm)) {
          covered.add(perm);
          addedCovered.push(perm);
        }
      }
      chosen.push(cand.name);

      dfs(i + 1, slotsLeft - 1);

      chosen.pop();
      for (const perm of addedCovered) covered.delete(perm);
      for (const perm of addedUnion) {
        union.delete(perm);
        if (!required.has(perm)) excessSoFar--;
      }
    }
  };

  dfs(0, limit);
  return bestRoles;
}
