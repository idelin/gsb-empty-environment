/** 租约领域模型。epoch 为乐观并发令牌：每次续租递增，过期/失效的续租请求会被拒绝。 */

export interface RegionLease {
  /** 根协调器颁发的粗粒度租约 ID */
  leaseId: string;
  regionId: string;
  /** 粗粒度配额：该区域可同时持有的子租约配额总量 */
  quota: number;
  issuedAt: number;
  expiresAt: number;
  epoch: number;
}

export interface SubLease {
  /** 区域网关颁发的细粒度子租约 ID */
  subLeaseId: string;
  workerId: string;
  regionId: string;
  quota: number;
  issuedAt: number;
  expiresAt: number;
  epoch: number;
}

export function isValid(lease: { expiresAt: number }, now: number): boolean {
  return lease.expiresAt > now;
}
