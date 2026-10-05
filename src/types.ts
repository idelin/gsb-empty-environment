export interface SimEvent {
  seq: number;
  id: string;
  kind: string;
  payload: Record<string, unknown>;
  at: number;
}

export interface SubscriptionFilter {
  kinds?: string[];
}

export interface WorldState {
  tick: number;
  prices: Record<string, number>;
  volume: Record<string, number>;
}

export interface SnapshotMessage {
  type: 'snapshot';
  epoch: number;
  baseSeq: number;
  state: WorldState;
  cursor: string;
}

export interface EventMessage {
  type: 'event';
  epoch: number;
  offset: number;
  replay: boolean;
  event: SimEvent;
  cursor: string;
}

export type ServerMessage = SnapshotMessage | EventMessage;
