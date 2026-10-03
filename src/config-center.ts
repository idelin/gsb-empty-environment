import type { Clock } from "./clock.ts";
import { isLeaseExpired, type Lease } from "./lease.ts";

/** 一份带版本号的集群配置。 */
export interface Config {
  version: number;
  data: Record<string, string>;
}

/** 下发给节点的绑定结果：新配置 + 与之绑定的新租约凭证。 */
export interface ConfigBinding {
  config: Config;
  lease: Lease;
}

export class LeaseRevokedError extends Error {
  constructor(leaseId: string) {
    super(`lease ${leaseId} has been revoked`);
    this.name = "LeaseRevokedError";
  }
}

export class StaleVersionError extends Error {
  constructor(leaseVersion: number, currentVersion: number) {
    super(
      `lease bound to stale config version ${leaseVersion}, current is ${currentVersion}`,
    );
    this.name = "StaleVersionError";
  }
}

interface LeaseRecord {
  lease: Lease;
  revoked: boolean;
}

/**
 * 配置中心：
 * - 维护当前配置版本；
 * - 向节点签发与配置版本绑定的租约；
 * - 配置更新时递增版本、撤销全部旧租约，并向在线节点推送新配置与新租约；
 * - 拒绝为旧版本租约续期（版本约束），强制节点重新绑定新配置。
 */
export class ConfigCenter {
  private config: Config;
  private readonly leaseTtlMs: number;
  private readonly clock: Clock;
  private readonly leases = new Map<string, LeaseRecord>();
  private readonly subscribers = new Map<string, (binding: ConfigBinding) => void>();
  private leaseSeq = 0;

  constructor(initialData: Record<string, string>, options: { leaseTtlMs: number; clock: Clock }) {
    this.config = { version: 1, data: { ...initialData } };
    this.leaseTtlMs = options.leaseTtlMs;
    this.clock = options.clock;
  }

  get currentVersion(): number {
    return this.config.version;
  }

  getCurrentConfig(): Config {
    return { version: this.config.version, data: { ...this.config.data } };
  }

  /** 节点注册推送通道；返回退订函数。 */
  subscribe(nodeId: string, push: (binding: ConfigBinding) => void): () => void {
    this.subscribers.set(nodeId, push);
    return () => {
      this.subscribers.delete(nodeId);
    };
  }

  /** 节点租用当前配置：返回当前配置与绑定该版本的新租约。 */
  rent(nodeId: string): ConfigBinding {
    const lease = this.issueLease(nodeId, this.config.version);
    return { config: this.getCurrentConfig(), lease };
  }

  /**
   * 续租。仅当租约未过期、未撤销、且绑定的配置版本仍为当前版本时才成功；
   * 否则抛出对应错误，节点必须重新 rent 以获取新配置。
   */
  renew(leaseId: string): Lease {
    const record = this.leases.get(leaseId);
    if (record === undefined) {
      throw new LeaseRevokedError(leaseId);
    }
    if (record.revoked) {
      throw new LeaseRevokedError(leaseId);
    }
    if (isLeaseExpired(record.lease, this.clock.now())) {
      throw new LeaseRevokedError(leaseId);
    }
    if (record.lease.configVersion !== this.config.version) {
      throw new StaleVersionError(record.lease.configVersion, this.config.version);
    }
    const renewed: Lease = {
      ...record.lease,
      expiresAt: this.clock.now() + this.leaseTtlMs,
    };
    this.leases.set(leaseId, { lease: renewed, revoked: false });
    return renewed;
  }

  /**
   * 修改配置（如数据库密码）：递增版本、撤销所有旧租约、
   * 向所有已订阅节点推送新配置与新租约凭证。
   * 脱网节点收不到推送，其旧租约将自然到期失效。
   */
  updateConfig(patch: Record<string, string>): Config {
    this.config = {
      version: this.config.version + 1,
      data: { ...this.config.data, ...patch },
    };
    for (const record of this.leases.values()) {
      record.revoked = true;
    }
    for (const [nodeId, push] of this.subscribers) {
      push({ config: this.getCurrentConfig(), lease: this.issueLease(nodeId, this.config.version) });
    }
    return this.getCurrentConfig();
  }

  /** 配置中心侧校验节点出示的租约凭证是否仍然有效（用于网关/存储侧二次校验）。 */
  validate(leaseId: string, configVersion: number): boolean {
    const record = this.leases.get(leaseId);
    if (record === undefined || record.revoked) {
      return false;
    }
    if (isLeaseExpired(record.lease, this.clock.now())) {
      return false;
    }
    return record.lease.configVersion === configVersion;
  }

  private issueLease(nodeId: string, configVersion: number): Lease {
    this.leaseSeq += 1;
    const now = this.clock.now();
    const lease: Lease = {
      leaseId: `lease-${this.leaseSeq}`,
      nodeId,
      configVersion,
      issuedAt: now,
      expiresAt: now + this.leaseTtlMs,
    };
    this.leases.set(lease.leaseId, { lease, revoked: false });
    return lease;
  }
}
