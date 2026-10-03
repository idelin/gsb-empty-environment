/**
 * Multi-Paxos 节点：
 *  - Phase 1: Prepare/Promise（竞选 + 日志收敛/补洞）
 *  - Phase 2: Accept/Accepted（多数派提交）
 *  - 领导者租约：租约有效期内跳过 Phase 1，直接以连续下标提交日志（无空洞）
 *  - 新领导者通过 Phase 1 收集多数派日志快照，补全空洞（NO_OP）并安全覆写少数派脏日志
 */
import type { Ballot, LogEntry, Message, Role, Value } from "./types.ts";
import { NO_OP, ballotToString, compareBallot } from "./types.ts";
import type { SimulatedNetwork } from "./network.ts";

export interface PaxosNodeOptions {
  id: number;
  peers: number[];
  network: SimulatedNetwork;
  now: () => number;
  leaseMs: number;
}

interface AcceptTracker {
  value: Value;
  acks: Set<number>;
}

export class PaxosNode {
  readonly id: number;
  private peers: number[];
  private network: SimulatedNetwork;
  private now: () => number;
  private leaseMs: number;

  role: Role = "follower";
  /** 已承诺的最大 ballot（acceptor 状态）。 */
  promised: Ballot = { term: 0, node: -1 };
  /** 稀疏日志：槽位 -> 条目（允许存在空洞，由领导者修复）。 */
  readonly log = new Map<number, LogEntry>();
  committedUpTo = 0;

  /** 领导者状态。 */
  private myBallot: Ballot | null = null;
  private leaseUntil = -1;
  private nextIndex = 1;
  private promises = new Map<number, { entries: LogEntry[]; committedUpTo: number }>();
  private acceptors = new Map<number, AcceptTracker>();
  private pendingValues: Value[] = [];

  /** 跟随者视角的领导者租约到期时间。 */
  private leaderLeaseUntil = -1;

  constructor(opts: PaxosNodeOptions) {
    this.id = opts.id;
    this.peers = opts.peers;
    this.network = opts.network;
    this.now = opts.now;
    this.leaseMs = opts.leaseMs;
    this.network.register(this.id, (m) => this.onMessage(m));
  }

  private get majority(): number {
    return Math.floor(this.peers.length / 2) + 1;
  }

  get isLeader(): boolean {
    return this.role === "leader";
  }

  get leaseValid(): boolean {
    return this.role === "leader" && this.now() < this.leaseUntil;
  }

  get followerLeaseValid(): boolean {
    return this.now() < this.leaderLeaseUntil;
  }

  /** 已提交条目（按下标升序；无空洞保证：1..committedUpTo 连续）。 */
  committedEntries(): LogEntry[] {
    const out: LogEntry[] = [];
    for (let i = 1; i <= this.committedUpTo; i++) {
      const e = this.log.get(i);
      if (e && e.committed) out.push(e);
    }
    return out;
  }

  // ---------- Phase 1: Prepare / Promise ----------

  /** 发起竞选（Phase 1）。 */
  campaign(): void {
    const term = Math.max(this.promised.term, this.myBallot?.term ?? 0) + 1;
    this.myBallot = { term, node: this.id };
    this.role = "candidate";
    this.promised = this.myBallot; // 候选者首先向自己承诺
    this.promises.clear();
    // 自己的一票直接计入（自链路消息不经过网络）。
    this.promises.set(this.id, {
      entries: [...this.log.values()].map((e) => ({ ...e })),
      committedUpTo: this.committedUpTo,
    });
    for (const p of this.peers) {
      if (p === this.id) continue;
      this.network.send({ type: "prepare", from: this.id, to: p, ballot: this.myBallot });
    }
    if (this.promises.size >= this.majority) this.becomeLeader();
  }

  private onPrepare(m: Extract<Message, { type: "prepare" }>): void {
    if (compareBallot(m.ballot, this.promised) < 0) return; // 已承诺更高 ballot，拒绝
    this.promised = m.ballot;
    if (this.role !== "follower" && this.myBallot && compareBallot(m.ballot, this.myBallot) > 0) {
      this.stepDown();
    }
    const entries: LogEntry[] = [];
    for (const e of this.log.values()) entries.push({ ...e });
    entries.sort((a, b) => a.index - b.index);
    this.network.send({
      type: "promise",
      from: this.id,
      to: m.from,
      ballot: m.ballot,
      entries,
      committedUpTo: this.committedUpTo,
    });
  }

  private onPromise(m: Extract<Message, { type: "promise" }>): void {
    if (this.role !== "candidate" || !this.myBallot) return;
    if (compareBallot(m.ballot, this.myBallot) !== 0) return;
    if (this.promises.has(m.from)) return;
    this.promises.set(m.from, { entries: m.entries, committedUpTo: m.committedUpTo });
    if (this.promises.size >= this.majority) this.becomeLeader();
  }

  // ---------- 领导者当选与日志修复（补洞 + 安全覆写） ----------

  private becomeLeader(): void {
    this.role = "leader";
    this.leaseUntil = this.now() + this.leaseMs;
    this.acceptors.clear();

    // 合并多数派（含自己）的日志视图：每个槽位取最高 ballot 的条目。
    const merged = new Map<number, LogEntry>();
    let maxIndex = 0;
    let maxCommitted = this.committedUpTo;
    const absorb = (entries: LogEntry[], committedUpTo: number) => {
      maxCommitted = Math.max(maxCommitted, committedUpTo);
      for (const e of entries) {
        maxIndex = Math.max(maxIndex, e.index);
        const cur = merged.get(e.index);
        if (
          !cur ||
          compareBallot(e.ballot, cur.ballot) > 0 ||
          (compareBallot(e.ballot, cur.ballot) === 0 && e.committed)
        ) {
          merged.set(e.index, { ...e });
        }
      }
    };
    absorb([...this.log.values()].map((e) => ({ ...e })), this.committedUpTo);
    for (const p of this.promises.values()) absorb(p.entries, p.committedUpTo);

    // 已被多数派提交过的前缀直接继承。
    for (let i = 1; i <= maxCommitted; i++) {
      const e = merged.get(i);
      if (e) this.log.set(i, { ...e, committed: true });
    }
    this.committedUpTo = maxCommitted;

    // 空洞修补：committedUpTo+1 .. maxIndex 连续覆盖，缺槽填 NO_OP；
    // 少数派脏日志（未被多数派接受、ballot 更低）在此被安全覆写。
    for (let i = maxCommitted + 1; i <= maxIndex; i++) {
      const e = merged.get(i);
      const value = e ? e.value : NO_OP;
      this.log.set(i, { index: i, ballot: this.myBallot!, value, committed: false });
      this.acceptors.set(i, { value, acks: new Set([this.id]) });
      for (const p of this.peers) {
        if (p === this.id) continue;
        this.network.send({ type: "accept", from: this.id, to: p, ballot: this.myBallot!, index: i, value });
      }
      this.maybeCommit(i);
    }
    this.nextIndex = maxIndex + 1;
    this.promises.clear();

    // 排空租约过期期间缓冲的提案。
    const pending = this.pendingValues;
    this.pendingValues = [];
    for (const v of pending) this.propose(v);
  }

  private stepDown(): void {
    if (this.role === "leader") {
      // 未完成的在途提案重新缓冲，待下次当选后重发。
      for (const [idx, t] of this.acceptors) {
        const e = this.log.get(idx);
        if (e && !e.committed && idx > this.committedUpTo) this.pendingValues.push(t.value);
      }
    }
    this.role = "follower";
    this.myBallot = null;
    this.acceptors.clear();
    this.leaseUntil = -1;
  }

  // ---------- Phase 2: Accept / Accepted（租约下绕过 Phase 1） ----------

  /**
   * 提案入口。领导者租约有效时直接 Phase 2，使用连续下标（无空洞）；
   * 租约过期则缓冲提案并重新发起 Phase 1。
   * 返回分配的槽位下标；本次未直接分配（非领导者或需重新竞选）返回 -1。
   */
  propose(value: Value): number {
    if (this.role !== "leader") return -1;
    if (!this.leaseValid) {
      this.pendingValues.push(value);
      this.campaign();
      return -1;
    }
    const index = this.nextIndex++;
    const ballot = this.myBallot!;
    this.log.set(index, { index, ballot, value, committed: false });
    this.acceptors.set(index, { value, acks: new Set([this.id]) });
    for (const p of this.peers) {
      if (p === this.id) continue;
      this.network.send({ type: "accept", from: this.id, to: p, ballot, index, value });
    }
    this.maybeCommit(index);
    return index;
  }

  /** 自票已够多数派（单机集群等）时立即提交。 */
  private maybeCommit(index: number): void {
    const tracker = this.acceptors.get(index);
    if (tracker && tracker.acks.size >= this.majority) {
      this.acceptors.delete(index);
      const e = this.log.get(index);
      if (e && !e.committed) {
        e.committed = true;
        this.advanceCommit();
      }
    }
  }

  private onAccept(m: Extract<Message, { type: "accept" }>): void {
    if (compareBallot(m.ballot, this.promised) < 0) return; // 旧领导者/过期 ballot，拒绝
    if (this.role !== "follower" && this.myBallot && compareBallot(m.ballot, this.myBallot) > 0) {
      this.stepDown();
    }
    this.promised = m.ballot;
    this.leaderLeaseUntil = this.now() + this.leaseMs;
    const existing = this.log.get(m.index);
    if (existing?.committed) {
      // 安全网：已提交条目永不被覆写（多数派相交性保证不会触发）。
      if (existing.value !== m.value) {
        throw new Error(`safety violation: committed entry ${m.index} overwrite attempt`);
      }
    } else {
      this.log.set(m.index, { index: m.index, ballot: m.ballot, value: m.value, committed: false });
    }
    this.network.send({ type: "accepted", from: this.id, to: m.from, ballot: m.ballot, index: m.index });
  }

  private onAccepted(m: Extract<Message, { type: "accepted" }>): void {
    if (this.role !== "leader" || !this.myBallot) return;
    if (compareBallot(m.ballot, this.myBallot) !== 0) return;
    const tracker = this.acceptors.get(m.index);
    if (!tracker) return;
    tracker.acks.add(m.from);
    if (tracker.acks.size >= this.majority) {
      this.acceptors.delete(m.index);
      const e = this.log.get(m.index);
      if (e && !e.committed) {
        e.committed = true;
        this.advanceCommit();
      }
    }
  }

  /** 连续提交推进：保证 committedUpTo 之前无空洞。 */
  private advanceCommit(): void {
    let idx = this.committedUpTo + 1;
    for (;;) {
      const e = this.log.get(idx);
      if (!e || !e.committed) break;
      this.committedUpTo = idx;
      idx++;
    }
    for (const p of this.peers) {
      if (p === this.id) continue;
      this.network.send({ type: "commit", from: this.id, to: p, ballot: this.myBallot!, committedUpTo: this.committedUpTo });
    }
  }

  private onCommit(m: Extract<Message, { type: "commit" }>): void {
    if (compareBallot(m.ballot, this.promised) < 0) return;
    if (this.role !== "follower" && this.myBallot && compareBallot(m.ballot, this.myBallot) > 0) {
      this.stepDown();
    }
    this.applyCommittedUpTo(m.committedUpTo, m.ballot);
    this.requestSyncIfBehind(m.from, m.committedUpTo);
  }

  /**
   * 提交水位超前但本地存在未提交/缺失槽位（空洞或脏日志）时，
   * 向对方请求权威的已提交条目。
   */
  private requestSyncIfBehind(peer: number, upTo: number): void {
    for (let i = 1; i <= upTo; i++) {
      const e = this.log.get(i);
      if (!e || !e.committed) {
        this.network.send({ type: "sync-request", from: this.id, to: peer, fromIndex: i });
        return;
      }
    }
  }

  private onSyncRequest(m: Extract<Message, { type: "sync-request" }>): void {
    const entries: LogEntry[] = [];
    for (let i = m.fromIndex; i <= this.committedUpTo; i++) {
      const e = this.log.get(i);
      if (e && e.committed) entries.push({ ...e });
    }
    const ballot = this.myBallot ?? this.promised;
    this.network.send({ type: "sync", from: this.id, to: m.from, ballot, entries, committedUpTo: this.committedUpTo });
  }

  private onSync(m: Extract<Message, { type: "sync" }>): void {
    if (compareBallot(m.ballot, this.promised) < 0) return;
    if (this.role !== "follower" && this.myBallot && compareBallot(m.ballot, this.myBallot) > 0) {
      this.stepDown();
    }
    this.promised = m.ballot;
    for (const e of m.entries) {
      const existing = this.log.get(e.index);
      if (existing?.committed) {
        if (existing.value !== e.value) {
          throw new Error(`safety violation: committed entry ${e.index} conflict on sync`);
        }
        continue;
      }
      // 已提交条目具有权威性：安全覆写本地未提交（脏）条目。
      this.log.set(e.index, { ...e, committed: true });
    }
    this.applyCommittedUpTo(m.committedUpTo, m.ballot);
  }

  /**
   * 应用提交水位：仅当本地条目与提交者同属一个 ballot（同一领导者任期，
   * 值具有权威性）时才标记已提交；否则留给 sync 用权威条目覆写。
   * committedUpTo 严格沿连续已提交前缀推进，保证无空洞。
   */
  private applyCommittedUpTo(upTo: number, ballot: Ballot): void {
    for (let i = 1; i <= upTo; i++) {
      const e = this.log.get(i);
      if (e && !e.committed && compareBallot(e.ballot, ballot) === 0) e.committed = true;
    }
    let idx = this.committedUpTo + 1;
    while (this.log.get(idx)?.committed) {
      this.committedUpTo = idx;
      idx++;
    }
  }

  /** 领导者心跳：续跟随者租约 + 同步提交水位。 */
  heartbeat(): void {
    if (!this.leaseValid) return;
    for (const p of this.peers) {
      if (p === this.id) continue;
      this.network.send({ type: "heartbeat", from: this.id, to: p, ballot: this.myBallot!, committedUpTo: this.committedUpTo });
    }
  }

  private onHeartbeat(m: Extract<Message, { type: "heartbeat" }>): void {
    if (compareBallot(m.ballot, this.promised) < 0) return;
    if (this.role !== "follower" && this.myBallot && compareBallot(m.ballot, this.myBallot) > 0) {
      this.stepDown();
    }
    this.leaderLeaseUntil = this.now() + this.leaseMs;
    this.applyCommittedUpTo(m.committedUpTo, m.ballot);
    this.requestSyncIfBehind(m.from, m.committedUpTo);
  }

  // ---------- 消息分发与状态 ----------

  private onMessage(m: Message): void {
    switch (m.type) {
      case "prepare": return this.onPrepare(m);
      case "promise": return this.onPromise(m);
      case "accept": return this.onAccept(m);
      case "accepted": return this.onAccepted(m);
      case "commit": return this.onCommit(m);
      case "heartbeat": return this.onHeartbeat(m);
      case "sync-request": return this.onSyncRequest(m);
      case "sync": return this.onSync(m);
    }
  }

  describe(): string {
    return `node${this.id}[${this.role} promised=${ballotToString(this.promised)} committed=${this.committedUpTo} log=${this.log.size}]`;
  }
}
