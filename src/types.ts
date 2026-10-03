export type Vote = 'YES' | 'NO';
export type Decision = 'COMMIT' | 'ABORT';
export type ParticipantState = 'PREPARED' | 'COMMITTED' | 'ABORTED' | 'UNKNOWN';

export type Message =
  | { type: 'prepare'; txId: string }
  | { type: 'vote'; txId: string; vote: Vote }
  | { type: 'decision'; txId: string; decision: Decision }
  | { type: 'ack'; txId: string }
  | { type: 'query'; txId: string }
  | { type: 'queryResponse'; txId: string; state: ParticipantState }
  | { type: 'statusRequest'; txId: string }
  | { type: 'statusResponse'; txId: string; decision: Decision };
