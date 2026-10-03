/**
 * Multi-Paxos 核心类型定义。
 * 仅使用可擦除语法（Node >= 23 类型剥离可直接运行）。
 */

/** 提案编号（ballot）：(term, nodeId) 字典序全序，保证全局唯一。 */
export interface Ballot {
  term: number;
  node: number;
}

export function compareBallot(a: Ballot, b: Ballot): number {
  if (a.term !== b.term) return a.term - b.term;
  return a.node - b.node;
}

export function ballotToString(b: Ballot): string {
  return `${b.term}.${b.node}`;
}

/** 日志值类型。NO_OP 用于领导者补洞时写入的空操作。 */
export type Value = string;

export const NO_OP: Value = "\0NO_OP\0";

export function isNoOp(v: Value): boolean {
  return v === NO_OP;
}

/** 单条日志。committed 标记该槽位是否已被多数派确认提交。 */
export interface LogEntry {
  index: number;
  ballot: Ballot;
  value: Value;
  committed: boolean;
}

export type Role = "follower" | "candidate" | "leader";

/** 节点间消息。 */
export type Message =
  | { type: "prepare"; from: number; to: number; ballot: Ballot }
  | {
      type: "promise";
      from: number;
      to: number;
      ballot: Ballot;
      /** 本节点已接受（含已提交）的全部日志条目快照，用于领导者补洞/收敛。 */
      entries: LogEntry[];
      committedUpTo: number;
    }
  | {
      type: "accept";
      from: number;
      to: number;
      ballot: Ballot;
      index: number;
      value: Value;
    }
  | { type: "accepted"; from: number; to: number; ballot: Ballot; index: number }
  | { type: "commit"; from: number; to: number; ballot: Ballot; committedUpTo: number }
  | { type: "sync-request"; from: number; to: number; fromIndex: number }
  | {
      type: "sync";
      from: number;
      to: number;
      ballot: Ballot;
      entries: LogEntry[];
      committedUpTo: number;
    }
  | { type: "heartbeat"; from: number; to: number; ballot: Ballot; committedUpTo: number };

export interface Clock {
  now(): number;
}
