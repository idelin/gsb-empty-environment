/**
 * 时态权限委托与继承解析器（双时态 RBAC 委托内核）
 *
 * 两个时间维度：
 *  - 业务时间 (valid time)：权限在现实业务中生效的时间区间 [from, to)
 *  - 审计时间 (transaction time)：系统“知悉”某条事实的时间区间 [from, to)
 *
 * 查询 (user, businessTime, auditTime) 表示：
 * “截至审计时间 auditTime 所掌握的信息来看，user 在业务时间 businessTime 是否持有权限”。
 *
 * 提前收回采用双时态更正：旧版本在 auditTime 关闭（不再可见），
 * 新版本自 auditTime 起生效、业务截止被截短到收回点。
 */

export const INF = Number.POSITIVE_INFINITY;

export type UserId = string;
export type RoleId = string;
export type Permission = string;

export interface Interval {
  readonly from: number;
  readonly to: number; // 半开区间终点，不含
}

const contains = (i: Interval, t: number): boolean => i.from <= t && t < i.to;

interface Versioned {
  /** 业务时间区间 */
  readonly valid: Interval;
  /** 审计时间区间 */
  tx: Interval;
}

export interface RoleGrant extends Versioned {
  readonly kind: 'grant';
  readonly user: UserId;
  readonly role: RoleId;
}

export interface Delegation extends Versioned {
  readonly kind: 'delegation';
  readonly from: UserId;
  readonly to: UserId;
  readonly role: RoleId;
}

export class TemporalDelegationKernel {
  private readonly roles = new Map<RoleId, Set<Permission>>();
  private readonly grants: RoleGrant[] = [];
  private readonly delegations: Delegation[] = [];

  /** 定义角色及其权限集。重复定义会合并权限。 */
  defineRole(role: RoleId, permissions: Iterable<Permission>): void {
    const set = this.roles.get(role) ?? new Set<Permission>();
    for (const p of permissions) set.add(p);
    this.roles.set(role, set);
  }

  /** 直接授予用户角色，业务生效区间 [validFrom, validTo)，自 txFrom 起知悉。 */
  grantRole(
    user: UserId,
    role: RoleId,
    validFrom: number,
    validTo: number = INF,
    txFrom: number = 0,
  ): RoleGrant {
    this.assertRole(role);
    this.assertInterval(validFrom, validTo);
    const grant: RoleGrant = {
      kind: 'grant', user, role,
      valid: { from: validFrom, to: validTo },
      tx: { from: txFrom, to: INF },
    };
    this.grants.push(grant);
    return grant;
  }

  /** 委托：from 把角色 role 委托给 to，业务区间 [validFrom, validTo)，自 txFrom 起知悉。 */
  delegate(
    from: UserId,
    to: UserId,
    role: RoleId,
    validFrom: number,
    validTo: number,
    txFrom: number = 0,
  ): Delegation {
    this.assertRole(role);
    this.assertInterval(validFrom, validTo);
    const d: Delegation = {
      kind: 'delegation', from, to, role,
      valid: { from: validFrom, to: validTo },
      tx: { from: txFrom, to: INF },
    };
    this.delegations.push(d);
    return d;
  }

  /**
   * 提前收回委托：在审计时间 auditTime 知悉“该委托于业务时间 businessTime 终止”。
   * 当前生效版本的审计区间在 auditTime 关闭，并写入截短后的新版本。
   */
  revokeDelegation(from: UserId, to: UserId, role: RoleId, businessTime: number, auditTime: number): void {
    const found = this.revoke(this.delegations, (d) => d.from === from && d.to === to && d.role === role, businessTime, auditTime);
    if (!found) throw new Error(`no active delegation ${from} -> ${to} for role ${role} at audit time ${auditTime}`);
  }

  /** 提前收回直接授权，语义同 revokeDelegation。 */
  revokeRoleGrant(user: UserId, role: RoleId, businessTime: number, auditTime: number): void {
    const found = this.revoke(this.grants, (g) => g.user === user && g.role === role, businessTime, auditTime);
    if (!found) throw new Error(`no active grant of ${role} to ${user} at audit time ${auditTime}`);
  }

  private revoke<T extends Versioned>(records: T[], match: (r: T) => boolean, businessTime: number, auditTime: number): boolean {
    let found = false;
    // 快照迭代：循环内会向 records 追加更正版本，不能边遍历边修改
    for (const r of [...records]) {
      if (!match(r) || r.tx.to !== INF || r.tx.from > auditTime) continue;
      found = true;
      r.tx = { from: r.tx.from, to: auditTime };
      if (businessTime > r.valid.from) {
        records.push({
          ...r,
          valid: { from: r.valid.from, to: Math.min(r.valid.to, businessTime) },
          tx: { from: auditTime, to: INF },
        });
      }
    }
    return found;
  }

  /** 判断 user 在 (businessTime, auditTime) 下是否实际持有角色 role（含委托链解析）。 */
  hasRole(user: UserId, role: RoleId, businessTime: number, auditTime: number): boolean {
    return this.resolve(user, role, businessTime, auditTime, new Set());
  }

  private resolve(user: UserId, role: RoleId, bt: number, at: number, visiting: Set<string>): boolean {
    const key = `${user}${role}`;
    if (visiting.has(key)) return false; // 委托环保护
    visiting.add(key);
    try {
      for (const g of this.grants) {
        if (g.user === user && g.role === role && contains(g.valid, bt) && contains(g.tx, at)) return true;
      }
      for (const d of this.delegations) {
        if (d.to !== user || d.role !== role) continue;
        if (!contains(d.valid, bt) || !contains(d.tx, at)) continue;
        // 委托链：委托方在同一 (bt, at) 下必须实际持有该角色
        if (this.resolve(d.from, role, bt, at, visiting)) return true;
      }
      return false;
    } finally {
      visiting.delete(key);
    }
  }

  /** user 在 (businessTime, auditTime) 下的全部实际有效角色。 */
  getEffectiveRoles(user: UserId, businessTime: number, auditTime: number): RoleId[] {
    const out: RoleId[] = [];
    for (const role of this.roles.keys()) {
      if (this.hasRole(user, role, businessTime, auditTime)) out.push(role);
    }
    return out.sort();
  }

  /** user 在 (businessTime, auditTime) 下的实际有效权限集合。 */
  getEffectivePermissions(user: UserId, businessTime: number, auditTime: number): Set<Permission> {
    const out = new Set<Permission>();
    for (const role of this.getEffectiveRoles(user, businessTime, auditTime)) {
      for (const p of this.roles.get(role)!) out.add(p);
    }
    return out;
  }

  private assertRole(role: RoleId): void {
    if (!this.roles.has(role)) throw new Error(`unknown role: ${role}`);
  }

  private assertInterval(from: number, to: number): void {
    if (!(from < to)) throw new Error(`invalid interval [${from}, ${to})`);
  }
}
