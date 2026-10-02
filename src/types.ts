export type EventType = 'join' | 'alive' | 'suspect' | 'leave';

export interface MemberEvent {
  type: EventType;
  nodeId: string;
  incarnation: number;
}

export type NodeStatus = 'alive' | 'suspect' | 'dead';

export interface MemberRecord {
  nodeId: string;
  status: NodeStatus;
  incarnation: number;
}

export function eventKey(event: MemberEvent): string {
  return `${event.type}:${event.nodeId}`;
}
