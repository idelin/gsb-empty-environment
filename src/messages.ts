// 消息与成员状态类型定义。
// 所有跨节点交换的报文都携带发送方的 DomainInfo 与 Lamport 时钟，
// 用于裂脑检测（partitionId 不一致）与时钟校正（Lamport 规则）。

export type MemberStatus = 'alive' | 'suspect' | 'dead';

export interface MemberState {
  id: number;
  status: MemberStatus;
  incarnation: number;
}

/** 分区域标识：由当前视图中存活集合确定性导出，同一子网内收敛后必然一致。 */
export interface DomainInfo {
  partitionId: string; // `P<minAliveId>`
  minAlive: number;
  aliveCount: number;
}

/** 间接探测（PingReq）的转发上下文。 */
export interface Via {
  origin: number;
  oseq: number;
}

interface BaseMsg {
  from: number;
  to: number;
  domain: DomainInfo;
  clock: number;
}

export interface PingMsg extends BaseMsg {
  kind: 'PING';
  seq: number;
  digest: MemberState[];
  via?: Via; // 存在即表示这是 PingReq 触发的间接 Ping
}

export interface AckMsg extends BaseMsg {
  kind: 'ACK';
  seq: number;
  digest: MemberState[];
  via?: Via;
}

export interface PingReqMsg extends BaseMsg {
  kind: 'PING_REQ';
  seq: number;
  target: number;
  digest: MemberState[];
}

export interface ReqAckMsg extends BaseMsg {
  kind: 'REQ_ACK';
  seq: number;
  target: number;
  ok: boolean;
  digest: MemberState[];
  targetDomain?: DomainInfo; // 目标的域信息（跨子网探活成功的关键证据）
}

export interface MergeProposeMsg extends BaseMsg {
  kind: 'MERGE_PROPOSE';
  view: MemberState[];
}

export interface MergeAcceptMsg extends BaseMsg {
  kind: 'MERGE_ACCEPT';
  view: MemberState[];
}

export interface MergeRejectMsg extends BaseMsg {
  kind: 'MERGE_REJECT';
}

export interface MergeCommitMsg extends BaseMsg {
  kind: 'MERGE_COMMIT';
  view: MemberState[];
  clockFloor: number; // 时钟校正下界：max(双方时钟)
}

export interface MergeDoneMsg extends BaseMsg {
  kind: 'MERGE_DONE';
}

export interface MergeFinalMsg extends BaseMsg {
  kind: 'MERGE_FINAL';
}

export type Message =
  | PingMsg
  | AckMsg
  | PingReqMsg
  | ReqAckMsg
  | MergeProposeMsg
  | MergeAcceptMsg
  | MergeRejectMsg
  | MergeCommitMsg
  | MergeDoneMsg
  | MergeFinalMsg;

export type MergeMessage =
  | MergeProposeMsg
  | MergeAcceptMsg
  | MergeRejectMsg
  | MergeCommitMsg
  | MergeDoneMsg
  | MergeFinalMsg;
