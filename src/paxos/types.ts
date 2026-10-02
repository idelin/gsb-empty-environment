/**
 * Multi-Paxos 核心类型定义。
 *
 * Ballot（提案编号）由 (round, nodeId) 构成字典序全序，
 * 保证任意两个提案编号可比较且全局唯一。
 */

export interface Ballot {
  readonly round: number;
  readonly nodeId: string;
}

export function compareBallot(a: Ballot, b: Ballot): number {
  if (a.round !== b.round) return a.round - b.round;
  return a.nodeId < b.nodeId ? -1 : a.nodeId > b.nodeId ? 1 : 0;
}

export function ballotToString(b: Ballot): string {
  return `${b.round}@${b.nodeId}`;
}

/** 日志值：NOOP 用于新 Leader 修补空洞槽位。 */
export const NOOP = Symbol.for("paxos.noop");
export type LogValue = unknown;

export interface LogEntry {
  readonly index: number;
  readonly ballot: Ballot;
  readonly value: LogValue;
}

export function isNoop(value: LogValue): boolean {
  return value === NOOP;
}

/** 已被 acceptor 接受的槽位记录（持久化状态）。 */
export interface AcceptedSlot {
  readonly ballot: Ballot;
  readonly value: LogValue;
}

// ---------- 消息 ----------

export interface PrepareMsg {
  readonly kind: "prepare";
  readonly ballot: Ballot;
  /** 只同步该下标之后的日志，避免全量传输。 */
  readonly fromIndex: number;
}

export interface PromiseMsg {
  readonly kind: "promise";
  readonly from: string;
  readonly ballot: Ballot;
  /** fromIndex 起该 acceptor 已接受的全部槽位（含空洞之间的）。 */
  readonly accepted: ReadonlyArray<{ index: number; ballot: Ballot; value: LogValue }>;
  readonly commitIndex: number;
}

export interface AcceptMsg {
  readonly kind: "accept";
  readonly ballot: Ballot;
  readonly index: number;
  readonly value: LogValue;
}

export interface AcceptedMsg {
  readonly kind: "accepted";
  readonly from: string;
  readonly ballot: Ballot;
  readonly index: number;
  /** 若该节点已承诺过更高的 ballot，则携带它以促使旧 Leader 退位。 */
  readonly promised: Ballot;
}

export interface RejectedMsg {
  readonly kind: "rejected";
  readonly from: string;
  readonly ballot: Ballot;
  readonly promised: Ballot;
}

/**
 * Leader 提交后广播，推进 follower 的 commitIndex。
 * prefixHash 是已提交前缀 [0..commitIndex] 的滚动哈希，
 * 用于 follower 的一致性检查（发现少数派脏日志）。
 */
export interface CommitMsg {
  readonly kind: "commit";
  readonly ballot: Ballot;
  readonly commitIndex: number;
  readonly prefixHash: number;
}

/** 已提交前缀的 FNV-1a 滚动哈希。 */
export function prefixHashOf(values: Iterable<LogValue>): number {
  let h = 0x811c9dc5;
  for (const v of values) {
    const s = v === NOOP ? "\0noop" : JSON.stringify(v) ?? String(v);
    for (let i = 0; i < s.length; i++) {
      h ^= s.charCodeAt(i);
      h = Math.imul(h, 0x01000193);
    }
    h ^= 0xff;
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

export type PaxosMsg =
  | PrepareMsg
  | PromiseMsg
  | AcceptMsg
  | AcceptedMsg
  | RejectedMsg
  | CommitMsg;
