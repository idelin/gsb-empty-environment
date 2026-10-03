/** 配置租约：将"使用某版本配置的权利"绑定到带 TTL 的凭证上。 */
export interface Lease {
  /** 租约唯一标识（凭证） */
  readonly leaseId: string;
  /** 承租节点 */
  readonly nodeId: string;
  /** 租约所绑定的配置版本（版本约束） */
  readonly configVersion: number;
  readonly issuedAt: number;
  /** 到期时间戳；到期后节点必须自行停止使用该配置，无需配置中心通知 */
  readonly expiresAt: number;
}

export function isLeaseExpired(lease: Lease, now: number): boolean {
  return now >= lease.expiresAt;
}
