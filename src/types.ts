export type NodeId = number;
export type MemberStatus = 'alive' | 'suspect' | 'dead';

export interface MemberInfo {
  id: NodeId;
  incarnation: number;
  status: MemberStatus;
  statusChangedAt: number;
}

export interface ViewSnapshot {
  epoch: number;
  members: MemberInfo[];
}

export type Message =
  | { type: 'ping'; from: NodeId; to: NodeId; clock: number; epoch: number; digest: string; replyTo: NodeId; origin: NodeId }
  | { type: 'ack'; from: NodeId; to: NodeId; clock: number; epoch: number; digest: string; forOrigin: NodeId }
  | { type: 'ping-req'; from: NodeId; to: NodeId; clock: number; target: NodeId }
  | { type: 'indirect-ack'; from: NodeId; to: NodeId; clock: number; epoch: number; digest: string; target: NodeId }
  | { type: 'merge-propose'; from: NodeId; to: NodeId; clock: number; epoch: number; digest: string; view: ViewSnapshot }
  | { type: 'merge-accept'; from: NodeId; to: NodeId; clock: number; epoch: number; digest: string; view: ViewSnapshot }
  | { type: 'merge-commit'; from: NodeId; to: NodeId; clock: number; epoch: number; view: ViewSnapshot }
  | { type: 'view-request'; from: NodeId; to: NodeId; clock: number }
  | { type: 'view-sync'; from: NodeId; to: NodeId; clock: number; epoch: number; view: ViewSnapshot };
