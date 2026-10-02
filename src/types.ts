/** 共享类型定义：分层租约模型 */

/** 根协调器颁发给区域网关的粗粒度租约（长周期，默认 60s） */
export interface RootLease {
  leaseId: string;
  gatewayId: string;
  /** 本租约授予的配额总量 */
  quotaTotal: number;
  /** 尚未被子租约占用的配额 */
  quotaAvailable: number;
  issuedAt: number;
  expiresAt: number;
  /** 乐观并发版本号，续租需携带以防过期租约被误续 */
  version: number;
}

/** 区域网关颁发给工作节点的细粒度子租约（短周期，默认 5s） */
export interface SubLease {
  subLeaseId: string;
  workerId: string;
  gatewayId: string;
  /** 该子租约占用的配额 */
  quota: number;
  issuedAt: number;
  expiresAt: number;
  version: number;
}

export type GrantResult =
  | { ok: true; lease: RootLease }
  | { ok: false; reason: string };

export type SubGrantResult =
  | { ok: true; subLease: SubLease }
  | { ok: false; reason: string };

/** 根协调器运行指标：用于验证“边缘抖动对根中心近乎零影响” */
export interface RootMetrics {
  /** 根中心处理的消息总数（租约请求/续租/释放） */
  messagesHandled: number;
  /** 根中心执行的租约操作数（CPU 负载代理指标） */
  leaseOperations: number;
  /** 当前活跃根租约数（应恒等于区域网关数） */
  activeRootLeases: number;
}
