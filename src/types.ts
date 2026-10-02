export type Role = 'follower' | 'candidate' | 'primary';

export type MessageKind =
  | 'RequestLease'
  | 'GrantLease'
  | 'RejectLease'
  | 'Heartbeat'
  | 'HeartbeatAck';

export interface Message {
  kind: MessageKind;
  from: string;
  to: string;
  epoch: number;
  leaseStart?: number;
  leaseEnd?: number;
  round?: number;
  deliverAt: number;
}

export interface SimEvent {
  tick: number;
  node: string;
  event: string;
  detail: string;
}
