import type { Clock } from "./clock.ts";
import type { Config, ConfigBinding, ConfigCenter } from "./config-center.ts";
import { isLeaseExpired, type Lease } from "./lease.ts";

export class NoValidLeaseError extends Error {
  constructor(nodeId: string) {
    super(`node ${nodeId} holds no valid config lease; refusing traffic`);
    this.name = "NoValidLeaseError";
  }
}

export class StaleConfigRejectedError extends Error {
  constructor(nodeVersion: number, incomingVersion: number) {
    super(
      `rejected config version ${incomingVersion}: not newer than local version ${nodeVersion}`,
    );
    this.name = "StaleConfigRejectedError";
  }
}

/**
 * 工作节点：
 * - 向配置中心租用配置，持有"配置 + 租约"绑定；
 * - 仅在持有未过期租约时对外接客（租约是本地可判定的，脱网也能正确熔断）；
 * - 版本约束：拒绝不高于本地版本的配置，防止旧配置重放；
 * - 续租失败（租约被撤销/版本过旧）时停止接客，需重新绑定。
 */
export class WorkerNode {
  readonly nodeId: string;
  private readonly center: ConfigCenter;
  private readonly clock: Clock;
  private readonly unsubscribe: () => void;
  private config: Config | undefined;
  private lease: Lease | undefined;

  constructor(nodeId: string, center: ConfigCenter, clock: Clock) {
    this.nodeId = nodeId;
    this.center = center;
    this.clock = clock;
    this.unsubscribe = center.subscribe(nodeId, (binding) => this.acceptBinding(binding));
  }

  /** 初始租用配置。 */
  connect(): void {
    const binding = this.center.rent(this.nodeId);
    this.applyBinding(binding);
  }

  /** 脱网：退订推送通道，模拟与配置中心失联。 */
  disconnect(): void {
    this.unsubscribe();
  }

  /** 本地租约是否仍然有效（无需联系配置中心即可判定）。 */
  hasValidLease(): boolean {
    return this.lease !== undefined && !isLeaseExpired(this.lease, this.clock.now());
  }

  get currentConfigVersion(): number {
    return this.config?.version ?? 0;
  }

  /** 续租：成功则延长本地租约；被撤销或版本过旧则清空租约并停止接客。 */
  renewLease(): void {
    if (this.lease === undefined) {
      return;
    }
    try {
      this.lease = this.center.renew(this.lease.leaseId);
    } catch {
      this.lease = undefined;
    }
  }

  /**
   * 对外接客：必须持有有效租约，否则拒绝服务，
   * 防止使用过期配置操作脏数据。
   */
  serve(query: string): { nodeId: string; configVersion: number; result: string } {
    if (!this.hasValidLease() || this.config === undefined || this.lease === undefined) {
      throw new NoValidLeaseError(this.nodeId);
    }
    return {
      nodeId: this.nodeId,
      configVersion: this.config.version,
      result: `served "${query}" with config v${this.config.version} (lease ${this.lease.leaseId})`,
    };
  }

  /** 读取当前生效配置中的某个键（如 db.password）。 */
  configValue(key: string): string | undefined {
    return this.config?.data[key];
  }

  /** 配置中心推送入口：带版本约束地接收新绑定。 */
  private acceptBinding(binding: ConfigBinding): void {
    this.applyBinding(binding);
  }

  private applyBinding(binding: ConfigBinding): void {
    const localVersion = this.config?.version ?? 0;
    if (binding.config.version <= localVersion) {
      throw new StaleConfigRejectedError(localVersion, binding.config.version);
    }
    if (binding.lease.configVersion !== binding.config.version) {
      throw new StaleConfigRejectedError(binding.config.version, binding.lease.configVersion);
    }
    if (isLeaseExpired(binding.lease, this.clock.now())) {
      throw new NoValidLeaseError(this.nodeId);
    }
    this.config = binding.config;
    this.lease = binding.lease;
  }
}
