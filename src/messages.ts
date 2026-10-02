export type NodeId = string;

/** 直连探测：from 探测 to。 */
export interface Ping {
  type: 'ping';
  seq: number;
  from: NodeId;
  to: NodeId;
}

/** 应答。target 表示“被探测的节点”：直连时等于 from，代理转发时为原目标。 */
export interface Ack {
  type: 'ack';
  seq: number;
  from: NodeId;
  to: NodeId;
  target: NodeId;
}

/** 间接探测请求：请求 to 代理探测 target。 */
export interface PingReq {
  type: 'ping-req';
  seq: number;
  from: NodeId;
  to: NodeId;
  target: NodeId;
}

export type Message = Ping | Ack | PingReq;
