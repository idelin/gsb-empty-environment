export type Tick = number;
export type WindowId = number;
export type NodeId = string;

export interface Grant {
  grantId: string;
  windowId: WindowId;
  nodeId: NodeId;
  amount: number;
  issuedAtTick: Tick;
  expiryTick: Tick;
}

export interface Allocation {
  grantId: string;
  amount: number;
}

export type Message =
  | { kind: "BorrowRequest"; requestId: string; nodeId: NodeId; amount: number }
  | { kind: "BorrowReject"; requestId: string; nodeId: NodeId; reason: string }
  | { kind: "GrantMsg"; grant: Grant; borrowRequestId: string }
  | {
      kind: "ConsumeReport";
      nodeId: NodeId;
      requestId: string;
      cost: number;
      tick: Tick;
      allocations: Allocation[];
    }
  | { kind: "ReturnReport"; returnId: string; nodeId: NodeId; grantId: string; amount: number }
  | { kind: "ReturnAck"; returnId: string; nodeId: NodeId; grantId: string; acceptedAmount: number };

export type RejectReason =
  | "insufficient_quota"
  | "request_id_cost_conflict"
  | "window_quota_exhausted";
