import type {
  DelegationVersion,
  GrantVersion,
  Permission,
  RecordVersion,
  Role,
  RoleId,
  UserId,
} from "./types.ts";

const OPEN = Number.POSITIVE_INFINITY;

function contains(interval: { from: number; to: number }, t: number): boolean {
  return interval.from <= t && t < interval.to;
}

/**
 * Bitemporal RBAC delegation kernel.
 *
 * Every fact (role grant / delegation) is stored as a version carrying two
 * half-open intervals:
 *  - valid: business time during which the fact is effective;
 *  - known: audit time during which the system believes the fact.
 *
 * Revocation never rewrites history: the currently-known version is closed
 * at the revocation's knowledge time, and a corrected version (with a
 * shortened business interval) becomes visible from that knowledge time on.
 */
export class TemporalDelegationKernel {
  private readonly roles = new Map<RoleId, Role>();
  private readonly versions: RecordVersion[] = [];

  registerRole(role: Role): void {
    this.roles.set(role.id, role);
  }

  /** Directly assign `role` to `user`, effective over business [validFrom, validTo), recorded at `knownAt`. */
  grantRole(user: UserId, role: RoleId, validFrom: number, validTo: number, knownAt: number): void {
    this.assertRole(role);
    this.assertInterval(validFrom, validTo);
    this.versions.push({
      kind: "grant",
      user,
      role,
      valid: { from: validFrom, to: validTo },
      known: { from: knownAt, to: OPEN },
    });
  }

  /** Delegate `role` from `delegator` to `delegatee` over business [validFrom, validTo), recorded at `knownAt`. */
  delegate(
    delegator: UserId,
    delegatee: UserId,
    role: RoleId,
    validFrom: number,
    validTo: number,
    knownAt: number,
  ): void {
    this.assertRole(role);
    this.assertInterval(validFrom, validTo);
    this.versions.push({
      kind: "delegation",
      delegator,
      delegatee,
      role,
      valid: { from: validFrom, to: validTo },
      known: { from: knownAt, to: OPEN },
    });
  }

  /**
   * Revoke the delegation `delegator -> delegatee` for `role`, effective at
   * business time `effectiveAt`, as known at audit time `knownAt`.
   */
  revokeDelegation(
    delegator: UserId,
    delegatee: UserId,
    role: RoleId,
    effectiveAt: number,
    knownAt: number,
  ): void {
    this.revoke(
      (v): v is DelegationVersion =>
        v.kind === "delegation" &&
        v.delegator === delegator &&
        v.delegatee === delegatee &&
        v.role === role,
      effectiveAt,
      knownAt,
      `no active delegation ${delegator} -> ${delegatee} for role ${role}`,
    );
  }

  /** Revoke a direct grant, effective at business time `effectiveAt`, as known at audit time `knownAt`. */
  revokeGrant(user: UserId, role: RoleId, effectiveAt: number, knownAt: number): void {
    this.revoke(
      (v): v is GrantVersion => v.kind === "grant" && v.user === user && v.role === role,
      effectiveAt,
      knownAt,
      `no active grant of role ${role} for user ${user}`,
    );
  }

  /** Does `user` effectively hold `role` at business time `businessTime`, as known at audit time `auditTime`? */
  hasRole(user: UserId, role: RoleId, businessTime: number, auditTime: number): boolean {
    return this.holds(user, role, businessTime, auditTime, new Set());
  }

  /** All roles effectively held by `user` at (businessTime, auditTime). */
  effectiveRoles(user: UserId, businessTime: number, auditTime: number): RoleId[] {
    return [...this.roles.keys()]
      .filter((role) => this.hasRole(user, role, businessTime, auditTime))
      .sort();
  }

  /** Union of permissions from all roles effectively held by `user` at (businessTime, auditTime). */
  effectivePermissions(user: UserId, businessTime: number, auditTime: number): Permission[] {
    const permissions = new Set<Permission>();
    for (const roleId of this.effectiveRoles(user, businessTime, auditTime)) {
      for (const permission of this.roles.get(roleId)!.permissions) {
        permissions.add(permission);
      }
    }
    return [...permissions].sort();
  }

  private revoke<T extends RecordVersion>(
    match: (v: RecordVersion) => v is T,
    effectiveAt: number,
    knownAt: number,
    errorMessage: string,
  ): void {
    const current = this.versions.filter(
      (v): v is T => match(v) && v.known.to === OPEN && contains(v.valid, effectiveAt),
    );
    if (current.length === 0) {
      throw new Error(`${errorMessage} (effective at business time ${effectiveAt})`);
    }
    for (const version of current) {
      version.known = { from: version.known.from, to: knownAt };
      this.versions.push({
        ...version,
        valid: { from: version.valid.from, to: effectiveAt },
        known: { from: knownAt, to: OPEN },
      });
    }
  }

  private holds(
    user: UserId,
    role: RoleId,
    businessTime: number,
    auditTime: number,
    visiting: Set<string>,
  ): boolean {
    const key = `${user}|${role}`;
    if (visiting.has(key)) return false; // delegation cycle
    visiting.add(key);
    try {
      for (const v of this.versions) {
        if (!contains(v.valid, businessTime) || !contains(v.known, auditTime)) continue;
        if (v.kind === "grant" && v.user === user && v.role === role) return true;
        if (
          v.kind === "delegation" &&
          v.delegatee === user &&
          v.role === role &&
          this.holds(v.delegator, role, businessTime, auditTime, visiting)
        ) {
          return true;
        }
      }
      return false;
    } finally {
      visiting.delete(key);
    }
  }

  private assertRole(role: RoleId): void {
    if (!this.roles.has(role)) throw new Error(`unknown role: ${role}`);
  }

  private assertInterval(from: number, to: number): void {
    if (!(from < to)) throw new Error(`invalid interval [${from}, ${to})`);
  }
}
