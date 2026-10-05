// 逻辑消息在两条链路上使用同一组连续序号。
export interface DataPacket {
  kind: "data";
  pathId: number; // 本次发送所在的链路
  seq: number; // 连续逻辑序号（跨路径不重置）
  sentAt: number;
  retransmit: boolean;
}

export interface AckPacket {
  kind: "ack";
  pathId: number; // ACK 实际到达发送方时所在的链路（合并校验的依据）
  cumAck: number; // 接收方已连续收到的最大序号
  sack: number[]; // 接收方缓存的乱序序号（选择性确认）
  echoSeq: number; // 触发本 ACK 的数据包序号（RTT 采样用）
  echoSentAt: number;
}

export type Packet = DataPacket | AckPacket;
