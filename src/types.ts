export type Vote = "YES" | "NO";
export type Decision = "COMMIT" | "ABORT";

export type TxnState =
  | "INIT"
  | "PREPARING"
  | "COMMITTING"
  | "ABORTING"
  | "COMMITTED"
  | "ABORTED";

export type ParticipantStatus =
  | "PREPARED"
  | "COMMITTED"
  | "ABORTED"
  | "UNKNOWN";

export type Message =
  | { type: "PREPARE"; txnId: string }
  | { type: "VOTE"; txnId: string; vote: Vote }
  | { type: "DECISION"; txnId: string; decision: Decision }
  | { type: "ACK"; txnId: string }
  | { type: "QUERY"; txnId: string }
  | { type: "STATUS"; txnId: string; status: ParticipantStatus };

export type WalRecord =
  | { op: "BEGIN"; txnId: string; participants: string[] }
  | { op: "VOTE"; txnId: string; participant: string; vote: Vote }
  | { op: "DECISION"; txnId: string; decision: Decision }
  | { op: "ACK"; txnId: string; participant: string }
  | { op: "END"; txnId: string };

export type AuditEvent = {
  event: "PENDING_PARTICIPANT";
  txnId: string;
  participant: string;
  retries: number;
  at: number;
};
