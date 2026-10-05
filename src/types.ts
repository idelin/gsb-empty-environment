export type NodeId = string;

export interface Heartbeat {
  nodeId: NodeId;
  generation: number;
  sentAt: number;
}

export type NodePhase = "alive" | "suspected" | "removed";
