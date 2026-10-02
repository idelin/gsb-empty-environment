export interface TransferMessage {
  kind: "transfer";
  id: number;
  from: string;
  to: string;
  amount: number;
  sentAt: number;
}

export interface MarkerMessage {
  kind: "marker";
  snapshotId: string;
  from: string;
  to: string;
}

export type Message = TransferMessage | MarkerMessage;
