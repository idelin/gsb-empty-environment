/** 节点健康状态 */
export type NodeState = 'alive' | 'suspect' | 'dead';

/** 节点动态元数据：机房 Zone、权重 Weight、角色 Role + 自由标签与属性 */
export interface NodeMetadata {
  zone: string;
  weight: number;
  role: string;
  labels: Record<string, string>;
  attributes: Record<string, string | number | boolean>;
}

/** 集群成员记录（本地视图） */
export interface NodeRecord {
  nodeId: string;
  address: string;
  metadata: NodeMetadata;
  /** 元数据版本号（Lamport 风格逻辑时钟，由节点自身单调递增） */
  version: number;
  /** 心跳计数（incarnation），用于健康判定 */
  heartbeat: number;
  /** 本地最近一次收到该节点心跳的时间戳（不参与 gossip 排序） */
  lastSeen: number;
  state: NodeState;
}

/** gossip 网络传输格式（不含本地时间戳） */
export interface WireRecord {
  nodeId: string;
  address: string;
  metadata: NodeMetadata;
  version: number;
  heartbeat: number;
  state: NodeState;
}

export function toWire(record: NodeRecord): WireRecord {
  return {
    nodeId: record.nodeId,
    address: record.address,
    metadata: structuredClone(record.metadata),
    version: record.version,
    heartbeat: record.heartbeat,
    state: record.state,
  };
}

/** 元数据的规范化摘要，用于同版本并发更新的确定性仲裁 */
export function metadataDigest(metadata: NodeMetadata): string {
  const sortObj = (obj: Record<string, unknown>): Record<string, unknown> =>
    Object.fromEntries(Object.entries(obj).sort(([a], [b]) => (a < b ? -1 : 1)));
  return JSON.stringify({
    zone: metadata.zone,
    weight: metadata.weight,
    role: metadata.role,
    labels: sortObj(metadata.labels),
    attributes: sortObj(metadata.attributes),
  });
}
