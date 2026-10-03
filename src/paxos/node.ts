import { AtomicCell } from '../atomic.ts';
import { compareBallot, ballotEquals, ZERO_BALLOT, type Ballot } from './ballot.ts';
import { NOOP, type LogEntry, type Message } from './types.ts';
import type { Network } from './network.ts';

export interface PaxosNodeOptions {
  id: string;
  peers: string[];
  network: Network;
  clock: () => number;
  leaseTtl: number;
}

interface Campaign {
  ballot: Ballot;
  promises: Map<string, { commitIndex: number; entries: LogEntry[] }>;
  rejects: number;
}

interface PendingProposal {
  slot: number;
  ballot: Ballot;
  value: unknown;
  acks: Set<string>;
}

/**
 * Multi-Paxos 节点：Prepare/Promise + Accept/Accepted 两阶段，
 * 领导者租约有效期内跳过 Phase 1 直接对连续槽位发起 Phase 2；
 * 新领导者当选后通过 Phase 1 收集多数派已接受条目，修补空洞并
 * 安全覆写少数派未提交的脏日志。所有共享状态迁移均通过
 * AtomicCell 的 CAS 完成，无全局互斥锁。
 */
export class PaxosNode {
  readonly id: string;

  #peers: string[];
  #network: Network;
  #clock: () => number;
  #leaseTtl: number;
  #majority: number;

  // 持久状态
  #promised = new AtomicCell<Ballot>(ZERO_BALLOT);
  #log = new Map<number, LogEntry>();
  #commitIndex = 0;
  #applied: unknown[] = [];

  // 领导者状态
  #leaderBallot: Ballot | null = null;
  #leaseExpiry = 0;
  #campaign: Campaign | null = null;
  #pending = new Map<number, PendingProposal>();
  #nextSlot = 1;
  #clientQueue: unknown[] = [];

  constructor(options: PaxosNodeOptions) {
    this.id = options.id;
    this.#peers = options.peers;
    this.#network = options.network;
    this.#clock = options.clock;
    this.#leaseTtl = options.leaseTtl;
    this.#majority = Math.floor(options.peers.length / 2) + 1;
  }

  get commitIndex(): number {
    return this.#commitIndex;
  }

  get promisedBallot(): Ballot {
    return this.#promised.load();
  }

  get leaderBallot(): Ballot | null {
    return this.#leaderBallot;
  }

  get isLeader(): boolean {
    return this.#leaderBallot !== null;
  }

  /** 租约有效：可绕过 Phase 1 直接提交连续日志条目。 */
  get leaseValid(): boolean {
    return this.isLeader && this.#clock() < this.#leaseExpiry;
  }

  get applied(): readonly unknown[] {
    return this.#applied;
  }

  get logEntries(): LogEntry[] {
    return [...this.#log.values()].sort((a, b) => a.slot - b.slot);
  }

  /** 尚未完成的在途提案数（泄漏断言用）。 */
  get inflightCount(): number {
    return this.#pending.size + this.#clientQueue.length + (this.#campaign === null ? 0 : 1);
  }

  // ---------------------------------------------------------------- 客户端 API

  /** 发起竞选（Phase 1）。 */
  campaign(): void {
    const promised = this.#promised.load();
    const ballot: Ballot = {
      round: Math.max(promised.round, this.#leaderBallot?.round ?? 0) + 1,
      node: this.id,
    };
    // 放弃旧任期领导权，避免双主
    this.#leaderBallot = null;
    this.#leaseExpiry = 0;
    this.#campaign = { ballot, promises: new Map(), rejects: 0 };
    this.#broadcast({ type: 'prepare', from: this.id, ballot, commitIndex: this.#commitIndex });
  }

  /**
   * 提交一个客户端值。租约有效时直接走 Phase 2（绕过 Phase 1）；
   * 否则先入队并触发竞选，当选后自动补交。
   */
  propose(value: unknown): void {
    if (!this.leaseValid) {
      this.#clientQueue.push(value);
      if (this.#campaign === null) this.campaign();
      return;
    }
    this.#startProposal(this.#nextSlot++, value);
  }

  // ---------------------------------------------------------------- 消息处理

  handle(msg: Message): void {
    switch (msg.type) {
      case 'prepare':
        this.#onPrepare(msg);
        break;
      case 'promise':
        this.#onPromise(msg);
        break;
      case 'promise-reject':
        this.#onPromiseReject(msg);
        break;
      case 'accept':
        this.#onAccept(msg);
        break;
      case 'accepted':
        this.#onAccepted(msg);
        break;
      case 'accept-reject':
        this.#onAcceptReject(msg);
        break;
      case 'commit':
        this.#onCommit(msg);
        break;
    }
  }

  /** Phase 1 受理解：更高选票则承诺，并回报未提交的已接受条目。 */
  #onPrepare(msg: Extract<Message, { type: 'prepare' }>): void {
    const promised = this.#promised.load();
    if (compareBallot(msg.ballot, promised) > 0) {
      this.#promised.update((cur) => (compareBallot(msg.ballot, cur) > 0 ? msg.ballot : cur));
      if (this.#leaderBallot && compareBallot(msg.ballot, this.#leaderBallot) > 0) {
        this.#stepDown();
      }
      const entries = [...this.#log.values()].filter((e) => e.slot > msg.commitIndex);
      this.#send(msg.from, {
        type: 'promise',
        from: this.id,
        ballot: msg.ballot,
        commitIndex: this.#commitIndex,
        entries,
      });
    } else {
      this.#send(msg.from, { type: 'promise-reject', from: this.id, ballot: msg.ballot, promised });
    }
  }

  #onPromise(msg: Extract<Message, { type: 'promise' }>): void {
    const campaign = this.#campaign;
    if (!campaign || !ballotEquals(msg.ballot, campaign.ballot)) return;
    if (campaign.promises.has(msg.from)) return;
    campaign.promises.set(msg.from, { commitIndex: msg.commitIndex, entries: msg.entries });
    if (campaign.promises.size >= this.#majority) this.#finalizeCampaign();
  }

  #onPromiseReject(msg: Extract<Message, { type: 'promise-reject' }>): void {
    const campaign = this.#campaign;
    if (!campaign || !ballotEquals(msg.ballot, campaign.ballot)) return;
    campaign.rejects += 1;
    if (campaign.rejects >= this.#majority) {
      // 多数派拒绝：存在更高选票，竞选失败
      this.#campaign = null;
    }
  }

  /** Phase 2 受理解：选票不低于承诺值则接受（覆写未提交条目）。 */
  #onAccept(msg: Extract<Message, { type: 'accept' }>): void {
    const promised = this.#promised.load();
    if (compareBallot(msg.ballot, promised) >= 0) {
      if (compareBallot(msg.ballot, promised) > 0) {
        this.#promised.update((cur) => (compareBallot(msg.ballot, cur) > 0 ? msg.ballot : cur));
      }
      if (msg.slot > this.#commitIndex) {
        // 已提交槽位永不覆写；未提交槽位允许被更高选票安全覆写
        this.#log.set(msg.slot, { slot: msg.slot, ballot: msg.ballot, value: msg.value });
      }
      this.#send(msg.from, { type: 'accepted', from: this.id, slot: msg.slot, ballot: msg.ballot });
    } else {
      this.#send(msg.from, { type: 'accept-reject', from: this.id, slot: msg.slot, promised });
    }
  }

  #onAccepted(msg: Extract<Message, { type: 'accepted' }>): void {
    const pending = this.#pending.get(msg.slot);
    if (!pending || !ballotEquals(pending.ballot, msg.ballot)) return;
    pending.acks.add(msg.from);
    if (pending.acks.size >= this.#majority) this.#tryCommit();
  }

  #onAcceptReject(msg: Extract<Message, { type: 'accept-reject' }>): void {
    if (this.#leaderBallot && compareBallot(msg.promised, this.#leaderBallot) > 0) {
      this.#stepDown();
    }
  }

  /** 提交通知：安装已提交条目（含空洞回填），推进连续提交前缀。 */
  #onCommit(msg: Extract<Message, { type: 'commit' }>): void {
    for (const entry of msg.entries) {
      if (this.#leaderBallot && compareBallot(entry.ballot, this.#leaderBallot) > 0) {
        this.#stepDown();
      }
      if (entry.slot > this.#commitIndex) {
        // 已提交条目是安全的：直接覆写本地未提交的脏条目
        this.#log.set(entry.slot, entry);
      }
    }
    while (this.#commitIndex < msg.commitIndex) {
      const entry = this.#log.get(this.#commitIndex + 1);
      if (!entry) break; // 等待后续 commit 消息回填
      this.#commitIndex += 1;
      this.#applied.push(entry.value);
    }
  }

  // ---------------------------------------------------------------- 内部逻辑

  /** 竞选成功：确立租约，修补空洞，补交客户端队列。 */
  #finalizeCampaign(): void {
    const campaign = this.#campaign;
    if (!campaign) return;
    this.#campaign = null;
    this.#leaderBallot = campaign.ballot;
    this.#leaseExpiry = this.#clock() + this.#leaseTtl;

    // 收集多数派（含自身）已接受的条目，按槽位取最高选票的值
    const chosen = new Map<number, LogEntry>();
    let maxSlot = this.#commitIndex;
    const consider = (entry: LogEntry): void => {
      if (entry.slot <= this.#commitIndex) return;
      maxSlot = Math.max(maxSlot, entry.slot);
      const current = chosen.get(entry.slot);
      if (!current || compareBallot(entry.ballot, current.ballot) > 0) {
        chosen.set(entry.slot, entry);
      }
    };
    for (const entry of this.#log.values()) consider(entry);
    for (const promise of campaign.promises.values()) {
      for (const entry of promise.entries) consider(entry);
    }

    // 空洞修补：连续覆盖 (commitIndex, maxSlot]，无条目槽位填 NOOP
    for (let slot = this.#commitIndex + 1; slot <= maxSlot; slot++) {
      const entry = chosen.get(slot);
      this.#startProposal(slot, entry ? entry.value : NOOP);
    }
    this.#nextSlot = maxSlot + 1;

    // 补交竞选期间积压的客户端值
    for (const value of this.#clientQueue.splice(0)) {
      this.#startProposal(this.#nextSlot++, value);
    }
  }

  #startProposal(slot: number, value: unknown): void {
    const ballot = this.#leaderBallot;
    if (!ballot) return;
    this.#pending.set(slot, { slot, ballot, value, acks: new Set() });
    this.#broadcast({ type: 'accept', from: this.id, slot, ballot, value });
  }

  /** 推进连续提交前缀：只有槽位连续的多数派已接受条目才能提交（无空洞）。 */
  #tryCommit(): void {
    const from = this.#commitIndex;
    for (;;) {
      const next = this.#commitIndex + 1;
      const pending = this.#pending.get(next);
      if (!pending || pending.acks.size < this.#majority) break;
      this.#pending.delete(next);
      this.#log.set(next, { slot: next, ballot: pending.ballot, value: pending.value });
      this.#commitIndex = next;
      this.#applied.push(pending.value);
    }
    if (this.#commitIndex > from) {
      // 多数派确认即续租
      this.#leaseExpiry = this.#clock() + this.#leaseTtl;
      const entries: LogEntry[] = [];
      for (const [slot, entry] of this.#log) {
        if (slot <= this.#commitIndex) entries.push(entry);
      }
      entries.sort((a, b) => a.slot - b.slot);
      this.#broadcast({ type: 'commit', from: this.id, commitIndex: this.#commitIndex, entries });
    }
  }

  #stepDown(): void {
    this.#leaderBallot = null;
    this.#leaseExpiry = 0;
    this.#campaign = null;
    this.#pending.clear();
  }

  #broadcast(msg: Omit<Message, 'to'> & { from: string }): void {
    for (const peer of this.#peers) {
      this.#network.send({ ...msg, to: peer } as Message);
    }
  }

  #send(to: string, msg: Omit<Message, 'to'> & { from: string }): void {
    this.#network.send({ ...msg, to } as Message);
  }
}
