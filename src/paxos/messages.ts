import type { Ballot } from "./ballot.ts";

export const NOOP = "<NOOP>";

export interface LogEntry {
  slot: number;
  ballot: Ballot;
  value: string;
}

export interface PrepareMsg {
  type: "Prepare";
  from: string;
  to: string;
  ballot: Ballot;
  fromSlot: number;
}

export interface PromiseMsg {
  type: "Promise";
  from: string;
  to: string;
  ballot: Ballot;
  entries: LogEntry[];
  commitIndex: number;
}

export interface AcceptMsg {
  type: "Accept";
  from: string;
  to: string;
  slot: number;
  ballot: Ballot;
  value: string;
  commitIndex: number;
}

export interface AcceptedMsg {
  type: "Accepted";
  from: string;
  to: string;
  slot: number;
  ballot: Ballot;
}

export interface CommitMsg {
  type: "Commit";
  from: string;
  to: string;
  ballot: Ballot;
  commitIndex: number;
}

export interface RequestSyncMsg {
  type: "RequestSync";
  from: string;
  to: string;
  fromSlot: number;
}

export interface SyncMsg {
  type: "Sync";
  from: string;
  to: string;
  ballot: Ballot;
  entries: LogEntry[];
  commitIndex: number;
}

export type Message =
  | PrepareMsg
  | PromiseMsg
  | AcceptMsg
  | AcceptedMsg
  | CommitMsg
  | RequestSyncMsg
  | SyncMsg;
