import type { Ballot } from './ballot.ts';

/** 日志条目：槽位内被接受的值及其选票。 */
export interface LogEntry {
  slot: number;
  ballot: Ballot;
  value: unknown;
}

/** 空洞修补时填充的空操作值。 */
export const NOOP: unique symbol = Symbol('paxos.noop');

export type Message =
  | { type: 'prepare'; from: string; to: string; ballot: Ballot; commitIndex: number }
  | { type: 'promise'; from: string; to: string; ballot: Ballot; commitIndex: number; entries: LogEntry[] }
  | { type: 'promise-reject'; from: string; to: string; ballot: Ballot; promised: Ballot }
  | { type: 'accept'; from: string; to: string; slot: number; ballot: Ballot; value: unknown }
  | { type: 'accepted'; from: string; to: string; slot: number; ballot: Ballot }
  | { type: 'accept-reject'; from: string; to: string; slot: number; promised: Ballot }
  | { type: 'commit'; from: string; to: string; commitIndex: number; entries: LogEntry[] };

export function formatValue(value: unknown): string {
  return value === NOOP ? '<noop>' : JSON.stringify(value);
}
