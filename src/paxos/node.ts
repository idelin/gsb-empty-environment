/**
 * Multi-Paxos 节点：Acceptor 持久状态 + Proposer 两阶段提交。
 *
 * - Phase 1 (Prepare/Promise)：竞选 Leader，收集多数派已接受日志。
 * - Phase 2 (Accept/Accepted)：提交日志槽位。
 * - Leader 租约：租约有效期内跳过 Phase 1，直接对连续下标发起 Phase 2。
 *   租约仅是活性优化，安全性完全由 ballot 保证：分区旧 Leader 的 Accept
 *   会被已承诺更高 ballot 的多数派拒绝。
 * - 空洞修补：新 Leader 在 Phase 1 后，对 [commitIndex+1, maxIndex] 内
 *   每个槽位取最高 ballot 的已接受值（已提交值必然被选中），
 *   无值槽位填 NOOP，重新执行 Phase 2，保证提交日志无空洞。
 */
import { InMemoryNetwork } from "./network.ts";
import {
  NOOP,
  compareBallot,
  type AcceptedSlot,
  type Ballot,
  type LogValue,
  type PaxosMsg,
  type PrepareMsg,
  type PromiseMsg,
  type AcceptMsg,
  type AcceptedMsg,
  type RejectedMsg,
  type CommitMsg,
  prefixHashOf,
} from "./types.ts";

export interface NodeOptions {
  leaseMs: number;
  now: () => number;
  /** 按 id 解析节点（用于 follower 向 Leader 请求日志同步）。 */
  resolvePeer: (id: string) => PaxosNode | undefined;
}

interface PendingSlot {
  value: LogValue;
  acks: Set<string>;
}

export class PaxosNode {
  readonly id: string;
  private readonly peers: string[];
  private readonly net: InMemoryNetwork;
  private readonly opts: NodeOptions;

  // ---- Acceptor 持久状态 ----
  promised: Ballot | null = null;
  readonly log = new Map<number, AcceptedSlot>();
  commitIndex = -1;

  // ---- Proposer / Leader 状态 ----
  private lastRound = 0;
  private leaderBallot: Ballot | null = null;
  private leaseUntil = 0;
  private nextIndex = 0;
  private readonly pending = new Map<number, PendingSlot>();
  private promises = new Map<string, PromiseMsg>();
  private campaigning: Ballot | null = null;
  private readonly chosen = new Set<number>();

  // 统计（测试断言用）
  preparesSent = 0;
  acceptsSent = 0;

  constructor(id: string, peers: string[], net: InMemoryNetwork, opts: NodeOptions) {
    this.id = id;
    this.peers = peers;
    this.net = net;
    this.opts = opts;
    net.register(id, (msg) => this.onMessage(msg));
  }

  private get quorum(): number {
    return Math.floor(this.peers.length / 2) + 1;
  }

  get isLeader(): boolean {
    return this.leaderBallot !== null;
  }

  get leaseValid(): boolean {
    return this.isLeader && this.opts.now() < this.leaseUntil;
  }

  get ballot(): Ballot | null {
    return this.leaderBallot;
  }

  /** 已连续提交的最大下标（无空洞不变量：提交前缀连续）。 */
  get committedPrefix(): number {
    return this.commitIndex;
  }

  committedEntries(): LogValue[] {
    const out: LogValue[] = [];
    for (let i = 0; i <= this.commitIndex; i++) {
      const slot = this.log.get(i);
      if (!slot) break;
      out.push(slot.value);
    }
    return out;
  }

  // ---------------- Proposer ----------------

  /** Phase 1：发起竞选。 */
  campaign(): void {
    this.lastRound++;
    const ballot: Ballot = { round: this.lastRound, nodeId: this.id };
    this.campaigning = ballot;
    this.promises = new Map();
    this.preparesSent++;
    this.handlePromise({ // 自投票
      kind: "promise",
      from: this.id,
      ballot,
      accepted: this.acceptedFrom(this.commitIndex + 1),
      commitIndex: this.commitIndex,
    });
    const msg: PrepareMsg = { kind: "prepare", ballot, fromIndex: this.commitIndex + 1 };
    this.net.broadcast(this.id, this.peers.filter((p) => p !== this.id), msg);
  }

  /**
   * 提交一个值。租约有效时走快速路径（仅 Phase 2）；
   * 否则先完成 Phase 1（含空洞修补）再提交。
   * 返回是否成功进入提交流程（分区中无法获得多数派时返回 false）。
   */
  propose(value: LogValue): boolean {
    if (!this.leaseValid) this.campaign();
    if (!this.isLeader) return false;
    const index = this.nextIndex++;
    this.phase2(index, value);
    return true;
  }

  /** Phase 2：广播 Accept 并统计 Accepted，多数派确认即提交。 */
  private phase2(index: number, value: LogValue): void {
    const ballot = this.leaderBallot;
    if (!ballot) return;
    this.pending.set(index, { value, acks: new Set([this.id]) });
    this.log.set(index, { ballot, value }); // Leader 自身先接受
    this.acceptsSent++;
    const msg: AcceptMsg = { kind: "accept", ballot, index, value };
    this.net.broadcast(this.id, this.peers.filter((p) => p !== this.id), msg);
    const slot = this.pending.get(index);
    if (slot && slot.acks.size >= this.quorum) {
      this.chosen.add(index);
      this.pending.delete(index);
    }
    this.tryAdvance();
  }

  /** 无空洞推进：仅当 commitIndex+1 已被多数派接受时前进。 */
  private tryAdvance(): void {
    let advanced = false;
    while (this.chosen.has(this.commitIndex + 1) && this.log.has(this.commitIndex + 1)) {
      this.commitIndex++;
      advanced = true;
    }
    if (advanced) this.broadcastCommit();
  }

  private broadcastCommit(): void {
    const ballot = this.leaderBallot;
    if (!ballot) return;
    const msg: CommitMsg = {
      kind: "commit",
      ballot,
      commitIndex: this.commitIndex,
      prefixHash: prefixHashOf(this.committedEntries()),
    };
    this.net.broadcast(this.id, this.peers.filter((p) => p !== this.id), msg);
  }

  /** 新 Leader 获得多数派 Promise 后执行空洞修补。 */
  private repair(): void {
    const merged = new Map<number, AcceptedSlot>();
    for (const p of this.promises.values()) {
      for (const e of p.accepted) {
        if (e.index <= this.commitIndex) continue;
        const cur = merged.get(e.index);
        if (!cur || compareBallot(e.ballot, cur.ballot) > 0) {
          merged.set(e.index, { ballot: e.ballot, value: e.value });
        }
      }
    }
    let maxIndex = this.commitIndex;
    for (const i of merged.keys()) if (i > maxIndex) maxIndex = i;
    // 对 [commitIndex+1, maxIndex] 逐槽位重新提案；无值槽位填 NOOP
    for (let i = this.commitIndex + 1; i <= maxIndex; i++) {
      const value = merged.get(i)?.value ?? NOOP;
      this.phase2(i, value);
    }
    this.nextIndex = maxIndex + 1;
  }

  // ---------------- 消息处理 ----------------

  private onMessage(msg: PaxosMsg): void {
    switch (msg.kind) {
      case "prepare": return this.onPrepare(msg);
      case "promise": return this.handlePromise(msg);
      case "accept": return this.onAccept(msg);
      case "accepted": return this.onAccepted(msg);
      case "rejected": return this.onRejected(msg);
      case "commit": return this.onCommit(msg);
    }
  }

  private acceptedFrom(fromIndex: number) {
    const out: { index: number; ballot: Ballot; value: LogValue }[] = [];
    for (const [index, slot] of this.log) {
      if (index >= fromIndex) out.push({ index, ballot: slot.ballot, value: slot.value });
    }
    out.sort((a, b) => a.index - b.index);
    return out;
  }

  private onPrepare(msg: PrepareMsg): void {
    if (this.promised && compareBallot(msg.ballot, this.promised) < 0) {
      const reply: RejectedMsg = {
        kind: "rejected", from: this.id, ballot: msg.ballot, promised: this.promised,
      };
      this.net.send(this.id, msg.ballot.nodeId, reply);
      return;
    }
    this.promised = msg.ballot;
    if (msg.ballot.round > this.lastRound) this.lastRound = msg.ballot.round;
    // 更高 ballot 的 Prepare 到来：旧 Leader 身份失效
    if (this.leaderBallot && compareBallot(msg.ballot, this.leaderBallot) > 0) {
      this.stepDown();
    }
    const reply: PromiseMsg = {
      kind: "promise",
      from: this.id,
      ballot: msg.ballot,
      accepted: this.acceptedFrom(msg.fromIndex),
      commitIndex: this.commitIndex,
    };
    this.net.send(this.id, msg.ballot.nodeId, reply);
  }

  private handlePromise(msg: PromiseMsg): void {
    if (!this.campaigning || compareBallot(msg.ballot, this.campaigning) !== 0) return;
    this.promises.set(msg.from, msg);
    if (this.promises.size < this.quorum) return;
    // 当选：建立租约，执行空洞修补
    this.leaderBallot = this.campaigning;
    this.campaigning = null;
    this.leaseUntil = this.opts.now() + this.opts.leaseMs;
    this.repair();
  }

  private onAccept(msg: AcceptMsg): void {
    if (this.promised && compareBallot(msg.ballot, this.promised) < 0) {
      const reply: RejectedMsg = {
        kind: "rejected", from: this.id, ballot: msg.ballot, promised: this.promised,
      };
      this.net.send(this.id, msg.ballot.nodeId, reply);
      return;
    }
    if (msg.ballot.round > this.lastRound) this.lastRound = msg.ballot.round;
    const existing = this.log.get(msg.index);
    if (msg.index <= this.commitIndex && existing) {
      // 已提交槽位：幂等确认，绝不覆写
    } else if (!existing || compareBallot(msg.ballot, existing.ballot) >= 0) {
      // 更高/相同 ballot 的 Accept 安全覆写（含少数派脏日志）
      this.log.set(msg.index, { ballot: msg.ballot, value: msg.value });
    }
    const reply: AcceptedMsg = {
      kind: "accepted",
      from: this.id,
      ballot: msg.ballot,
      index: msg.index,
      promised: this.promised ?? msg.ballot,
    };
    this.net.send(this.id, msg.ballot.nodeId, reply);
  }

  private onAccepted(msg: AcceptedMsg): void {
    if (!this.leaderBallot || compareBallot(msg.ballot, this.leaderBallot) !== 0) return;
    if (compareBallot(msg.promised, this.leaderBallot) > 0) {
      this.stepDown(); // 对端已承诺更高 ballot
      return;
    }
    const slot = this.pending.get(msg.index);
    if (!slot) return;
    slot.acks.add(msg.from);
    if (slot.acks.size >= this.quorum) {
      this.chosen.add(msg.index);
      this.pending.delete(msg.index);
      this.tryAdvance();
    }
  }

  private onRejected(msg: RejectedMsg): void {
    if (msg.promised.round > this.lastRound) this.lastRound = msg.promised.round;
    if (this.leaderBallot && compareBallot(msg.promised, this.leaderBallot) > 0) {
      this.stepDown();
    }
    if (this.campaigning && compareBallot(msg.promised, this.campaigning) > 0) {
      this.campaigning = null; // 竞选失败
    }
  }

  private onCommit(msg: CommitMsg): void {
    // 更高 ballot 的 Commit：旧 Leader 身份失效
    if (this.leaderBallot && compareBallot(msg.ballot, this.leaderBallot) > 0) {
      this.stepDown();
    }
    if (msg.ballot.round > this.lastRound) this.lastRound = msg.ballot.round;
    const target = Math.min(msg.commitIndex, this.highestContiguousAccepted());
    if (target < msg.commitIndex) {
      // 本地缺少已提交槽位（少数派缺日志）：请求 Leader 同步
      this.opts.resolvePeer(msg.ballot.nodeId)?.serveSync(this.id);
      return;
    }
    // 一致性检查：已提交前缀哈希必须一致，否则说明本地有脏日志，
    // 不推进 commitIndex，请求 Leader 用更高 ballot 的 Accept 覆写。
    const mine: LogValue[] = [];
    for (let i = 0; i <= target; i++) mine.push(this.log.get(i)!.value);
    if (prefixHashOf(mine) !== msg.prefixHash) {
      this.opts.resolvePeer(msg.ballot.nodeId)?.serveSync(this.id);
      return;
    }
    if (target > this.commitIndex) {
      this.commitIndex = target;
      for (let i = 0; i <= this.commitIndex; i++) this.chosen.add(i);
    }
  }

  private highestContiguousAccepted(): number {
    let i = -1;
    while (this.log.has(i + 1)) i++;
    return i;
  }

  /**
   * Leader 响应同步请求：向 follower 重发所有已提交槽位的 Accept
   * （更高 ballot 安全覆写少数派脏日志），随后重发 Commit。
   */
  serveSync(followerId: string): void {
    if (!this.leaderBallot) return;
    for (let i = 0; i <= this.commitIndex; i++) {
      const slot = this.log.get(i);
      if (!slot) continue;
      const msg: AcceptMsg = { kind: "accept", ballot: this.leaderBallot, index: i, value: slot.value };
      this.net.send(this.id, followerId, msg);
    }
    const commit: CommitMsg = {
      kind: "commit",
      ballot: this.leaderBallot,
      commitIndex: this.commitIndex,
      prefixHash: prefixHashOf(this.committedEntries()),
    };
    this.net.send(this.id, followerId, commit);
  }

  private stepDown(): void {
    this.leaderBallot = null;
    this.leaseUntil = 0;
    this.pending.clear();
  }
}
