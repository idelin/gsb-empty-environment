export type NodeId = string;

export type NodeStatus = 'alive' | 'dead';

/** A single membership state record. Higher `version` always wins (last-writer-wins). */
export interface NodeState {
  nodeId: NodeId;
  status: NodeStatus;
  version: number;
  updatedBy: NodeId;
}

/** Lightweight gossip message: recent events piggybacked onto a periodic ping. */
export interface GossipMessage {
  kind: 'gossip';
  from: NodeId;
  events: NodeState[];
}
