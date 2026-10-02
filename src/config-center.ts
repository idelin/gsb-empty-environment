import type { Clock } from "./clock.ts";
import { systemClock } from "./clock.ts";
import { LeaseBinder } from "./lease-binder.ts";
import type { ClusterConfig, Lease, LeaseValidation } from "./lease.ts";

/** 配置订阅者（工作节点）：配置中心下发新配置与新租约的回调接口。 */
export interface ConfigSubscriber {
  readonly nodeId: string;
  onConfigPush(config: ClusterConfig, lease: Lease): void;
}

export interface ConfigCenterOptions {
  leaseTtlMs: number;
  clock?: Clock;
  initialConfig?: Omit<ClusterConfig, "version">;
}

export interface PublishResult {
  version: number;
  revokedLeases: number;
  delivered: string[];
  undelivered: string[];
}

/**
 * 配置中心：持有当前生效配置，签发/校验/撤销租约，
 * 配置变更时撤销旧版本租约并向在线节点推送新配置与新租约。
 */
export class ConfigCenter {
  private readonly binder: LeaseBinder;
  private readonly subscribers = new Map<string, ConfigSubscriber>();
  private config: ClusterConfig;

  constructor(options: ConfigCenterOptions) {
    const clock: Clock = options.clock ?? systemClock;
    this.binder = new LeaseBinder({ ttlMs: options.leaseTtlMs, clock });
    this.config = { version: 1, ...(options.initialConfig ?? { dbPassword: "initial" }) };
  }

  get currentConfig(): Readonly<ClusterConfig> {
    return this.config;
  }

  /** 节点上线：注册推送通道。 */
  register(subscriber: ConfigSubscriber): void {
    this.subscribers.set(subscriber.nodeId, subscriber);
  }

  /** 节点脱网/下线：移除推送通道。 */
  unregister(nodeId: string): void {
    this.subscribers.delete(nodeId);
  }

  /** 节点租用当前版本配置，返回配置与租约凭证。 */
  rent(nodeId: string): { config: ClusterConfig; lease: Lease } {
    const lease = this.binder.issue(nodeId, this.config.version);
    return { config: { ...this.config }, lease };
  }

  /** 节点续租：带版本约束，旧版本租约一律拒绝。 */
  renewLease(leaseId: string): LeaseValidation {
    return this.binder.renew(leaseId, this.config.version);
  }

  /** 校验租约是否对当前版本有效（如网关在转发前校验）。 */
  validateLease(leaseId: string): LeaseValidation {
    return this.binder.validate(leaseId, this.config.version);
  }

  /**
   * 修改配置并发布：版本 +1，撤销全部旧版本租约，
   * 向所有在线节点下发新配置与新租约凭证。
   * 脱网节点收不到推送，其旧租约已作废，最迟在 TTL 到期后自然失效。
   */
  updateConfig(patch: Partial<Omit<ClusterConfig, "version">>): PublishResult {
    const oldVersion = this.config.version;
    this.config = { ...this.config, ...patch, version: oldVersion + 1 };

    const revokedLeases = this.binder.revokeVersion(oldVersion);

    const delivered: string[] = [];
    const undelivered: string[] = [];
    for (const subscriber of this.subscribers.values()) {
      try {
        const lease = this.binder.issue(subscriber.nodeId, this.config.version);
        subscriber.onConfigPush({ ...this.config }, lease);
        delivered.push(subscriber.nodeId);
      } catch {
        undelivered.push(subscriber.nodeId);
      }
    }
    return { version: this.config.version, revokedLeases, delivered, undelivered };
  }
}
