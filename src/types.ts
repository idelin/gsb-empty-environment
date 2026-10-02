export type ShardId = string;
export type Lsn = number;
export type TxnId = string;
export type AccountId = string;

export interface AccountOp {
  account: AccountId;
  delta: number;
}

export interface LocalCommitEvent {
  kind: 'local-commit';
  shard: ShardId;
  lsn: Lsn;
  txnId: TxnId;
  ops: AccountOp[];
}

export interface ParticipantCommitEvent {
  kind: 'participant-commit';
  shard: ShardId;
  lsn: Lsn;
  txnId: TxnId;
  participants: ShardId[];
  ops: AccountOp[];
}

export interface HeartbeatEvent {
  kind: 'heartbeat';
  shard: ShardId;
  lsn: Lsn;
}

export type CdcEvent = LocalCommitEvent | ParticipantCommitEvent | HeartbeatEvent;
