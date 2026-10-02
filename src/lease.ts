/** 配置租约：将某个配置版本绑定给某个节点，带 TTL。 */
export interface Lease {
  leaseId: string;
  nodeId: string;
  configVersion: number;
  issuedAt: number;
  expiresAt: number;
  revoked: boolean;
}

export type LeaseInvalidReason =
  | "unknown-lease"
  | "revoked"
  | "expired"
  | "version-mismatch";

export type LeaseValidation =
  | { ok: true; lease: Lease }
  | { ok: false; reason: LeaseInvalidReason };

/** 集群动态配置。version 由配置中心单调递增维护。 */
export interface ClusterConfig {
  version: number;
  dbPassword: string;
  [key: string]: unknown;
}
