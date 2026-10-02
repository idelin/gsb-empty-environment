/** 业务消息：节点间的一笔转账。 */
export interface TransferMessage {
  kind: 'transfer';
  /** 全局唯一消息 id（便于审计/断言）。 */
  id: number;
  from: string;
  to: string;
  amount: number;
  /** 发送方在发送时刻的逻辑时钟（用于一致性割断言）。 */
  senderClock: number;
}

/** 控制消息：Chandy-Lamport Marker。 */
export interface MarkerMessage {
  kind: 'marker';
  snapshotId: string;
  from: string;
  to: string;
}

export type Message = TransferMessage | MarkerMessage;
