import type { Ballot } from './ballot.ts';

export type NodeId = string;
export type Value = string;

/** Reserved filler value a new leader proposes to seal log holes. */
export const NOOP: Value = '<noop>';

/** An entry stored in the acceptor log. `acceptedAt` is logical time (test oracle only). */
export interface LogEntry {
  readonly ballot: Ballot;
  readonly value: Value;
  readonly acceptedAt: number;
}

/** A slot reported by an acceptor inside a Promise message. */
export interface AcceptedSlot {
  readonly slot: number;
  readonly ballot: Ballot;
  readonly value: Value;
}

export interface Prepare {
  readonly type: 'Prepare';
  readonly from: NodeId;
  readonly to: NodeId;
  readonly ballot: Ballot;
}

export interface PromiseMsg {
  readonly type: 'Promise';
  readonly from: NodeId;
  readonly to: NodeId;
  readonly ballot: Ballot;
  /** Every slot this acceptor has ever accepted (committed or not). */
  readonly accepted: readonly AcceptedSlot[];
}

export interface Accept {
  readonly type: 'Accept';
  readonly from: NodeId;
  readonly to: NodeId;
  readonly ballot: Ballot;
  readonly slot: number;
  readonly value: Value;
  /** Piggybacked leader commit index. */
  readonly commitIndex: number;
}

export interface Accepted {
  readonly type: 'Accepted';
  readonly from: NodeId;
  readonly to: NodeId;
  readonly ballot: Ballot;
  readonly slot: number;
}

export interface Commit {
  readonly type: 'Commit';
  readonly from: NodeId;
  readonly to: NodeId;
  readonly ballot: Ballot;
  readonly commitIndex: number;
}

export interface Heartbeat {
  readonly type: 'Heartbeat';
  readonly from: NodeId;
  readonly to: NodeId;
  readonly ballot: Ballot;
  readonly commitIndex: number;
}

export interface HeartbeatAck {
  readonly type: 'HeartbeatAck';
  readonly from: NodeId;
  readonly to: NodeId;
  readonly ballot: Ballot;
  /** Follower's committed prefix length; lets the leader re-send missing slots. */
  readonly appliedLen: number;
}

export type Message = Prepare | PromiseMsg | Accept | Accepted | Commit | Heartbeat | HeartbeatAck;

export type MessageType = Message['type'];
