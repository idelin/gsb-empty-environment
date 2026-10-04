export type NodeId = string;

export type HealthState = "ALIVE" | "SUSPECTED" | "REMOVED";

export interface Heartbeat {
  nodeId: NodeId;
  epoch: number;
  seq: number;
  sentAt: number;
}

export type SimEvent =
  | { kind: "send-heartbeat"; at: number; nodeId: NodeId }
  | { kind: "deliver-heartbeat"; at: number; hb: Heartbeat }
  | { kind: "evaluate"; at: number }
  | { kind: "pause-node"; at: number; nodeId: NodeId }
  | { kind: "resume-node"; at: number; nodeId: NodeId }
  | { kind: "crash-node"; at: number; nodeId: NodeId }
  | { kind: "restart-node"; at: number; nodeId: NodeId };
