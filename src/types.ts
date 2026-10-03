/** 节点元数据：Bootstrap 快照与 Gossip 样本中交换的最小单元。 */
export interface NodeMeta {
  /** 集群内唯一节点 ID */
  id: string;
  /** 传输层可达地址 */
  address: string;
  /** 是否为静态 Seed 节点 */
  isSeed: boolean;
  /** 单调递增心跳，用于合并时判断条目新鲜度 */
  heartbeat: number;
  /** 节点加入集群的物理时间（毫秒） */
  joinedAt: number;
}

export interface BootstrapRequest {
  type: 'BOOTSTRAP';
  from: NodeMeta;
}

export interface BootstrapAck {
  type: 'BOOTSTRAP_ACK';
  from: NodeMeta;
  /** 应答方当前已知的全网成员快照 */
  snapshot: NodeMeta[];
}

export interface ShuffleRequest {
  type: 'SHUFFLE';
  from: NodeMeta;
  /** 发起方从洗牌环中抽出的成员样本 */
  sample: NodeMeta[];
}

export interface ShuffleReply {
  type: 'SHUFFLE_REPLY';
  from: NodeMeta;
  sample: NodeMeta[];
}

export type Message = BootstrapRequest | BootstrapAck | ShuffleRequest | ShuffleReply;
