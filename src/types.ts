/** 全局写入租约。term 为单调递增的 fencing token，用于杜绝过期持有者继续写入。 */
export interface Lease {
  holder: string;
  term: number;
  /** 租约到期时刻（epoch ms）。过期后任何持有者都不得再写入。 */
  expiresAt: number;
}

export interface AcquireResponse {
  granted: boolean;
  lease: Lease | null;
  reason?: string;
}

export interface WitnessStatus {
  now: number;
  lease: Lease | null;
}

/**
 * primary : 持有有效租约，唯一可写角色
 * standby : 只读备机，可尝试获取/接管租约
 * fenced  : 检测到法官凭证已失效的旧主机，永久只读（需人工 reset 才可重新参与竞选）
 */
export type Role = 'primary' | 'standby' | 'fenced';
