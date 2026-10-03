export type Role = 'FOLLOWER' | 'CANDIDATE' | 'PRIMARY';

export type MessageType =
  | 'LeaseRequest'
  | 'LeaseAck'
  | 'LeaseReject'
  | 'Heartbeat'
  | 'HeartbeatAck';

export interface Message {
  readonly id: number;
  readonly type: MessageType;
  readonly from: string;
  readonly to: string;
  readonly term: number;
  readonly sendTick: number;
  readonly deliverTick: number;
  readonly reason?: string;
}

export interface LeaseConfig {
  readonly nodeIds: string[];
  /** 租约时长(tick) */
  readonly leaseTtl: number;
  /** 受界时钟漂移:任意两节点时钟的最大差值(tick) */
  readonly maxClockSkew: number;
  /** LeaseRequest / Heartbeat 的单向投递延迟(tick);Ack 类消息同 tick 生效 */
  readonly networkDelay: number;
  /** 主节点心跳间隔(tick) */
  readonly heartbeatInterval: number;
  /** 候选者选举重试间隔(tick) */
  readonly electionRetryInterval: number;
  /** 备节点选举退避单位(tick),按优先级排位倍增,避免同时竞选 */
  readonly electionBackoffUnit: number;
}

export interface SimEvent {
  readonly tick: number;
  readonly node: string | null;
  readonly type: string;
  readonly detail: string;
}
