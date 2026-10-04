// Shared message and result types for the edge cache simulator.
//
// The origin distributes three kinds of messages over the bus:
//   - invalidate: "a newer version exists" (may arrive before the data)
//   - barrier:    "reads must now satisfy version >= barrier, or wait"
//   - data:       the actual payload for a version
//
// Messages for the same key may be delivered out of order.

export interface InvalidateMsg {
  kind: 'invalidate';
  key: string;
  version: number;
}

export interface BarrierMsg {
  kind: 'barrier';
  key: string;
  version: number;
}

export interface DataMsg {
  kind: 'data';
  key: string;
  version: number;
  value: unknown;
}

export type Message = InvalidateMsg | BarrierMsg | DataMsg;

// Point-in-time view of a key at the origin, used for resync on reconnect.
export interface Snapshot {
  key: string;
  version: number;
  value: unknown;
  hasValue: boolean;
  barrier: number;
}

export interface ReadResult {
  value: unknown;
  version: number;
}

// Predicate used by the bus to deliver selected queued messages.
export type MessagePred = (msg: Message, to: string) => boolean;
