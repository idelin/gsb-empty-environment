import type { Clock } from "./clock.ts";
import { systemClock } from "./clock.ts";
import type { Lease, LeaseValidation } from "./lease.ts";

export interface LeaseBinderOptions {
  /** 租约时长（毫秒）。节点脱网后旧租约最多存活这么久。 */
  ttlMs: number;
  clock?: Clock;
}

/**
 * 带版本约束的配置信息租约绑定器（配置中心侧）。
 *
 * 不变式：一张租约只在「未撤销 且 未过期 且 版本等于当前生效版本」时有效。
 * 版本约束保证：配置一旦升级，旧版本租约立即作废（对能连到中心的节点生效）；
 * TTL 保证：脱网节点的旧租约也会自然失效。
 */
export class LeaseBinder {
  private readonly ttlMs: number;
  private readonly clock: Clock;
  private readonly leases = new Map<string, Lease>();
  private nextId = 1;

  constructor(options: LeaseBinderOptions) {
    if (options.ttlMs <= 0) throw new Error("ttlMs must be positive");
    this.ttlMs = options.ttlMs;
    this.clock = options.clock ?? systemClock;
  }

  /** 为节点签发一张绑定到指定配置版本的租约。 */
  issue(nodeId: string, configVersion: number): Lease {
    const now = this.clock.now();
    const lease: Lease = {
      leaseId: `lease-${this.nextId++}`,
      nodeId,
      configVersion,
      issuedAt: now,
      expiresAt: now + this.ttlMs,
      revoked: false,
    };
    this.leases.set(lease.leaseId, lease);
    return { ...lease };
  }

  /** 校验租约是否可用于当前版本 requiredVersion。 */
  validate(leaseId: string, requiredVersion: number): LeaseValidation {
    const lease = this.leases.get(leaseId);
    if (!lease) return { ok: false, reason: "unknown-lease" };
    if (lease.revoked) return { ok: false, reason: "revoked" };
    if (this.clock.now() >= lease.expiresAt) return { ok: false, reason: "expired" };
    if (lease.configVersion !== requiredVersion) {
      return { ok: false, reason: "version-mismatch" };
    }
    return { ok: true, lease: { ...lease } };
  }

  /** 续租：仅当租约对当前版本仍有效时延长 TTL，否则返回失败原因。 */
  renew(leaseId: string, requiredVersion: number): LeaseValidation {
    const result = this.validate(leaseId, requiredVersion);
    if (!result.ok) return result;
    const lease = this.leases.get(leaseId)!;
    lease.expiresAt = this.clock.now() + this.ttlMs;
    return { ok: true, lease: { ...lease } };
  }

  /** 撤销某张租约（节点下线、主动释放等）。 */
  revokeLease(leaseId: string): boolean {
    const lease = this.leases.get(leaseId);
    if (!lease || lease.revoked) return false;
    lease.revoked = true;
    return true;
  }

  /** 撤销绑定到某个配置版本的全部租约，返回撤销数量。 */
  revokeVersion(configVersion: number): number {
    let count = 0;
    for (const lease of this.leases.values()) {
      if (!lease.revoked && lease.configVersion === configVersion) {
        lease.revoked = true;
        count++;
      }
    }
    return count;
  }
}
