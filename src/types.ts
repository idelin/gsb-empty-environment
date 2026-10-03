/** 节点角色 */
export type NodeRole = 'leader' | 'follower' | 'observer';

/** 节点健康状态 */
export type HealthStatus = 'healthy' | 'suspect' | 'dead';

/** 节点动态元数据 */
export interface NodeMetadata {
  /** 机房 / 可用区 */
  zone: string;
  /** 负载权重 (0-100)，路由优先选择权重高者 */
  weight: number;
  /** 集群角色 */
  role: NodeRole;
  /** 扩展标签（异构属性） */
  labels: Record<string, string>;
}

/** 带版本号的元数据记录，用于并发更新仲裁 */
export interface VersionedMetadata {
  meta: NodeMetadata;
  /** 单调递增版本号（Lamport 时钟） */
  version: number;
  /** 最后更新该元数据的节点 ID（版本冲突时的仲裁 tie-breaker） */
  updatedBy: string;
  /** 更新时间戳（毫秒，仅作展示/调试） */
  updatedAt: number;
}

/** 成员列表中的节点条目 */
export interface MemberNode {
  id: string;
  address: string;
  health: HealthStatus;
  /** 健康状态版本（与元数据版本独立演进） */
  healthVersion: number;
  record: VersionedMetadata;
}

/** 路由筛选条件：键值相等匹配，支持 meta 字段与 labels */
export type AttributeFilter = Record<string, string>;

/** Gossip 摘要条目：节点 -> 版本向量 */
export interface DigestEntry {
  id: string;
  metaVersion: number;
  healthVersion: number;
}
