export type NodeLifecycle = "active" | "leaving" | "gone";

export interface LeaveIntentMessage {
  readonly type: "leave-intent";
  readonly nodeId: string;
  readonly quiescentMs: number;
  readonly sentAt: number;
}

export interface LeaveCompleteMessage {
  readonly type: "leave-complete";
  readonly nodeId: string;
  readonly sentAt: number;
}

export type ClusterMessage = LeaveIntentMessage | LeaveCompleteMessage;
