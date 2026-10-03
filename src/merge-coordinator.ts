import { resolveMerge } from './membership.ts';
import type {
  DomainInfo,
  MemberState,
  MergeAcceptMsg,
  MergeCommitMsg,
  MergeDoneMsg,
  MergeFinalMsg,
  MergeMessage,
  MergeProposeMsg,
  MergeRejectMsg,
  Message,
} from './messages.ts';

export type MergeState = 'idle' | 'proposing' | 'committing';

/** 宿主节点提供给协调器的能力（由 PeerNode 实现）。 */
export interface MergeHost {
  readonly id: number;
  partitionInfo(): DomainInfo;
  isForeignContact(domain: DomainInfo, peerId: number): boolean;
  viewSnapshot(): MemberState[];
  clockTick(): number;
  send(msg: Message): void;
  installMergedView(view: MemberState[], clockFloor: number): void;
  onMergeEvent(event: string, detail: string): void;
}

export interface MergeOptions {
  timeout: number;    // 握手阶段超时（tick）
  maxRetries: number; // 阶段最大重试次数（有界 => 不会无限等待）
  cooldown: number;   // 被拒绝后的冷却（避免提案风暴）
}

const DEFAULT_OPTIONS: MergeOptions = { timeout: 6, maxRetries: 4, cooldown: 4 };

/**
 * 域优先级全序：存活数多者优先；并列时 minAlive 小者优先。
 * 双方各自独立计算必然得到相同胜者 —— 这是无死锁的关键。
 * 返回值 > 0 表示 a 胜出。
 */
export function compareDomains(a: DomainInfo, b: DomainInfo): number {
  if (a.aliveCount !== b.aliveCount) return a.aliveCount - b.aliveCount;
  return b.minAlive - a.minAlive;
}

/** 合并两个成员视图（确定性，双方计算结果一致）。 */
export function mergeViews(a: MemberState[], b: MemberState[], selfId: number): MemberState[] {
  const merged = new Map<number, MemberState>();
  for (const entry of [...a, ...b]) {
    const current = merged.get(entry.id);
    merged.set(entry.id, current ? { ...resolveMerge(current, entry) } : { ...entry });
  }
  const self = merged.get(selfId);
  if (self && self.status !== 'alive') {
    self.status = 'alive';
    self.incarnation += 1;
  }
  return [...merged.values()].sort((x, y) => x.id - y.id);
}

export function domainOf(view: MemberState[]): DomainInfo {
  const alive = view
    .filter((m) => m.status === 'alive')
    .map((m) => m.id)
    .sort((x, y) => x - y);
  const minAlive = alive[0] ?? 0;
  return { partitionId: `P${minAlive}`, minAlive, aliveCount: alive.length };
}

/**
 * 为合并视图加盖“合并纪元”：所有条目的 incarnation 提升到 max+1。
 * 使提交前在途的旧摘要（低 incarnation）在 gossip 中必然落败，
 * 防止过期信息毒化已合并视图。双方独立计算结果一致。
 */
export function stampMergeEpoch(view: MemberState[]): MemberState[] {
  const epoch = Math.max(0, ...view.map((m) => m.incarnation)) + 1;
  return view.map((m) => ({ ...m, incarnation: epoch }));
}

/**
 * 裂脑恢复合并协调器（每节点一个实例）。
 *
 * 三阶段握手：
 *   1. PROPOSE  发现异域存活节点后发起，携带本域视图与时钟；
 *   2. ACCEPT/REJECT  按确定性优先级裁决 —— 同时提案时低优先级方放弃并 ACCEPT，
 *      高优先级方 REJECT 对方并继续自己的提案（不存在互相等待）；
 *   3. COMMIT -> DONE -> FINAL  胜出方广播合并视图与时钟下界，全员应用后确认。
 *
 * 无死锁保证：
 *   - 优先级是全序，同时提案必然收敛到唯一胜者；
 *   - 每个阶段都有超时 + 有界重试，耗尽后回退 idle（可被后续心跳重新触发）；
 *   - COMMIT 幂等，重复/乱序提交不会破坏状态。
 */
export class MergeCoordinator {
  state: MergeState = 'idle';
  mergesCompleted = 0;
  private proposeTarget = 0;
  private deadline = 0;
  private retries = 0;
  private cooldownUntil = 0;
  private doneFrom = new Set<number>();
  private mergedView: MemberState[] = [];
  private mergedDomain: DomainInfo | undefined;
  /** 每个分区已应用的最低 committer id：重复 COMMIT 时只应用最小 id 的视图。 */
  private appliedCommitters = new Map<string, number>();

  private readonly host: MergeHost;
  private readonly opts: MergeOptions;

  constructor(host: MergeHost, opts: MergeOptions = DEFAULT_OPTIONS) {
    this.host = host;
    this.opts = opts;
  }

  /** 发现异域存活节点（首次跨子网心跳打通）时触发。 */
  trigger(remote: DomainInfo, remoteId: number, tick: number, via: string): void {
    if (this.state !== 'idle' || tick < this.cooldownUntil) return;
    if (!this.host.isForeignContact(remote, remoteId)) return;
    this.host.onMergeEvent('foreign-contact', `via=${via} peer=${remoteId} remote=${remote.partitionId}`);
    this.startPropose(remoteId, tick);
  }

  handle(msg: MergeMessage, tick: number): void {
    switch (msg.kind) {
      case 'MERGE_PROPOSE':
        this.onPropose(msg, tick);
        break;
      case 'MERGE_ACCEPT':
        this.onAccept(msg, tick);
        break;
      case 'MERGE_REJECT':
        this.onReject(msg, tick);
        break;
      case 'MERGE_COMMIT':
        this.onCommit(msg, tick);
        break;
      case 'MERGE_DONE':
        this.onDone(msg, tick);
        break;
      case 'MERGE_FINAL':
        this.onFinal(msg);
        break;
    }
  }

  /** 超时与有界重试：保证任何阶段都不会无限阻塞。 */
  onTick(tick: number): void {
    if (this.state === 'idle' || tick <= this.deadline) return;
    if (this.retries >= this.opts.maxRetries) {
      this.host.onMergeEvent('abort', `state=${this.state}`);
      this.state = 'idle';
      this.cooldownUntil = tick + this.opts.cooldown;
      return;
    }
    this.retries++;
    this.deadline = tick + this.opts.timeout;
    if (this.state === 'proposing') {
      this.host.onMergeEvent('retry', `propose->${this.proposeTarget}`);
      this.sendPropose(this.proposeTarget);
    } else {
      this.host.onMergeEvent('retry', 'commit-resend');
      this.sendCommits();
    }
  }

  private startPropose(target: number, tick: number): void {
    this.state = 'proposing';
    this.proposeTarget = target;
    this.retries = 0;
    this.deadline = tick + this.opts.timeout;
    this.host.onMergeEvent('propose', `->${target}`);
    this.sendPropose(target);
  }

  private sendPropose(target: number): void {
    this.host.send({
      kind: 'MERGE_PROPOSE',
      from: this.host.id,
      to: target,
      domain: this.host.partitionInfo(),
      clock: this.host.clockTick(),
      view: this.host.viewSnapshot(),
    });
  }

  private onPropose(msg: MergeProposeMsg, tick: number): void {
    const mine = this.host.partitionInfo();
    if (!this.host.isForeignContact(msg.domain, msg.from)) return;
    if (compareDomains(msg.domain, mine) > 0) {
      // 对方域胜出：放弃自己的提案（若有），接受对方。
      this.state = 'idle';
      this.host.onMergeEvent('accept', `remote=${msg.domain.partitionId} from=${msg.from}`);
      this.host.send({
        kind: 'MERGE_ACCEPT',
        from: this.host.id,
        to: msg.from,
        domain: mine,
        clock: this.host.clockTick(),
        view: this.host.viewSnapshot(),
      });
    } else {
      // 本域胜出：拒绝对方；若自己尚未提案则立即反提案。
      this.host.onMergeEvent('reject', `remote=${msg.domain.partitionId} from=${msg.from}`);
      this.host.send({
        kind: 'MERGE_REJECT',
        from: this.host.id,
        to: msg.from,
        domain: mine,
        clock: this.host.clockTick(),
      });
      if (this.state === 'idle') this.startPropose(msg.from, tick);
    }
  }

  private onAccept(msg: MergeAcceptMsg, tick: number): void {
    if (this.state !== 'proposing') return;
    const merged = stampMergeEpoch(mergeViews(this.host.viewSnapshot(), msg.view, this.host.id));
    this.mergedView = merged;
    this.mergedDomain = domainOf(merged);
    this.state = 'committing';
    this.retries = 0;
    this.doneFrom = new Set([this.host.id]);
    this.appliedCommitters.set(this.mergedDomain.partitionId, this.host.id);
    this.host.installMergedView(merged, this.host.clockTick());
    this.host.onMergeEvent('commit', `members=${merged.length} domain=${this.mergedDomain.partitionId}`);
    this.sendCommits();
    this.deadline = tick + this.opts.timeout;
  }

  private sendCommits(): void {
    if (!this.mergedDomain) return;
    const domain = this.mergedDomain;
    for (const member of this.mergedView) {
      if (member.id === this.host.id || this.doneFrom.has(member.id)) continue;
      const clock = this.host.clockTick();
      this.host.send({
        kind: 'MERGE_COMMIT',
        from: this.host.id,
        to: member.id,
        domain,
        clock,
        view: this.mergedView,
        clockFloor: clock,
      });
    }
  }

  private onCommit(msg: MergeCommitMsg, _tick: number): void {
    const mine = this.host.partitionInfo();
    // 只接受来自更高优先级域（或合并后同域）的提交。
    if (msg.domain.partitionId !== mine.partitionId && compareDomains(msg.domain, mine) <= 0) return;
    const lowest = this.appliedCommitters.get(msg.domain.partitionId);
    if (lowest !== undefined && msg.from > lowest) {
      // 已应用更优先 committer 的视图；仍回 DONE 让冗余 committer 正常收尾。
      this.host.send({
        kind: 'MERGE_DONE',
        from: this.host.id,
        to: msg.from,
        domain: mine,
        clock: this.host.clockTick(),
      });
      return;
    }
    this.appliedCommitters.set(msg.domain.partitionId, msg.from);
    this.state = 'idle';
    this.cooldownUntil = 0;
    this.host.installMergedView(msg.view, msg.clockFloor);
    this.host.onMergeEvent('commit-applied', `from=${msg.from} domain=${msg.domain.partitionId}`);
    this.host.send({
      kind: 'MERGE_DONE',
      from: this.host.id,
      to: msg.from,
      domain: this.host.partitionInfo(),
      clock: this.host.clockTick(),
    });
  }

  private onDone(msg: MergeDoneMsg, _tick: number): void {
    if (this.state !== 'committing') return;
    this.doneFrom.add(msg.from);
    if (this.mergedView.every((m) => this.doneFrom.has(m.id))) {
      this.state = 'idle';
      this.mergesCompleted++;
      this.host.onMergeEvent('final', `done=${this.doneFrom.size}`);
      for (const member of this.mergedView) {
        if (member.id === this.host.id) continue;
        this.host.send({
          kind: 'MERGE_FINAL',
          from: this.host.id,
          to: member.id,
          domain: this.host.partitionInfo(),
          clock: this.host.clockTick(),
        });
      }
    }
  }

  private onFinal(msg: MergeFinalMsg): void {
    this.state = 'idle';
    this.host.onMergeEvent('finalized', `from=${msg.from}`);
  }

  private onReject(msg: MergeRejectMsg, tick: number): void {
    if (this.state !== 'proposing') return;
    this.state = 'idle';
    this.cooldownUntil = tick + this.opts.cooldown;
    this.host.onMergeEvent('rejected', `from=${msg.from}`);
  }
}
