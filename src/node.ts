import { LamportClock } from './lamport.ts';
import { Membership } from './membership.ts';
import { MergeCoordinator, domainOf, type MergeHost, type MergeState } from './merge-coordinator.ts';
import type { SimulatedNetwork } from './network.ts';
import type {
  AckMsg,
  DomainInfo,
  MemberState,
  Message,
  PingMsg,
  PingReqMsg,
  ReqAckMsg,
} from './messages.ts';

// 协议时序参数（单位：tick）
const PROBE_INTERVAL = 2;    // 环上探测间隔
const ACK_TIMEOUT = 2;       // 直接 Ping 超时
const REQ_TIMEOUT = 4;       // PingReq 整体超时
const SUSPECT_TIMEOUT = 6;   // suspect -> dead
const REATTACH_INTERVAL = 10; // 死亡节点重接探测间隔
const INDIRECT_K = 2;        // 间接探测中介数

export type NodeLogger = (nodeId: number, event: string, detail: string, tick: number) => void;

interface PendingPing {
  target: number;
  sentTick: number;
}

interface PendingIndirect {
  origin: number;
  oseq: number;
  target: number;
  sentTick: number;
}

interface PendingReq {
  target: number;
  kind: 'probe' | 'reattach';
  sentTick: number;
}

/**
 * SWIM 风格节点：
 * - 存活节点按 id 排序成环（闭环），每节点周期探测后继；
 * - 直接 Ping 超时后经由 k 个中介 PingReq 间接探测，再失败则 suspect -> dead；
 * - 死亡节点定期通过 PingReq 重接探测 —— 分区愈合后，首个成功的跨子网
 *   PingReq 即触发合并握手；
 * - 合并协议委托给 MergeCoordinator。
 */
export class PeerNode implements MergeHost {
  readonly clock = new LamportClock();
  private readonly membership: Membership;
  private readonly merge: MergeCoordinator;
  private seq = 0;
  private currentTick = 0;
  private lastPartitionId = '';
  private pendingPings = new Map<number, PendingPing>();
  private indirectPending = new Map<number, PendingIndirect>();
  private pendingReqs = new Map<number, PendingReq>();
  private probeTrack = new Map<number, number>(); // target -> 未决间接探测数
  private suspectSince = new Map<number, number>();

  readonly id: number;
  private readonly network: SimulatedNetwork;
  private readonly logger: NodeLogger;

  constructor(id: number, allIds: number[], network: SimulatedNetwork, logger: NodeLogger) {
    this.id = id;
    this.network = network;
    this.logger = logger;
    this.membership = new Membership(allIds);
    this.merge = new MergeCoordinator(this);
    this.network.register(id, (msg, tick) => this.handle(msg, tick));
    this.lastPartitionId = this.partitionInfo().partitionId;
  }

  // ---- MergeHost 实现 ----

  partitionInfo(): DomainInfo {
    return domainOf(this.membership.all());
  }

  /**
   * 异域接触判定（同时满足）：
   * 1. 域标识不同，且对方域的最小存活成员不在我的存活集合中
   *    （若在，说明该域已被合并吸收，属过期报文）；
   * 2. 对端节点本身在我视图中非存活
   *    （存活对端的域差异只是子网收敛过渡态，交给 gossip 处理）。
   */
  isForeignContact(domain: DomainInfo, peerId: number): boolean {
    if (domain.partitionId === this.partitionInfo().partitionId) return false;
    if (this.membership.aliveIds().includes(domain.minAlive)) return false;
    return this.membership.get(peerId).status !== 'alive';
  }

  viewSnapshot(): MemberState[] {
    return this.membership.all();
  }

  clockTick(): number {
    return this.clock.tick();
  }

  clockRead(): number {
    return this.clock.read();
  }

  send(msg: Message): void {
    this.network.send(msg);
  }

  installMergedView(view: MemberState[], clockFloor: number): void {
    this.membership.install(view);
    const self = this.membership.get(this.id);
    if (self.status !== 'alive') {
      // 自反驳安全网：合并视图不应判死自己
      self.status = 'alive';
      self.incarnation += 1;
    }
    this.clock.witness(clockFloor); // 时钟校正：至少推进到双方最大值
    this.suspectSince.clear();
    this.noteDomainChange();
  }

  onMergeEvent(event: string, detail: string): void {
    this.log(event, detail);
  }

  get mergeState(): MergeState {
    return this.merge.state;
  }

  get mergesCompleted(): number {
    return this.merge.mergesCompleted;
  }

  aliveCount(): number {
    return this.membership.aliveCount();
  }

  viewString(): string {
    return JSON.stringify(this.membership.all());
  }

  /** 环上的后继（闭环：最大 id 的后继回绕到最小 id）。 */
  ringSuccessor(): number | undefined {
    const alive = this.membership.aliveIds();
    if (alive.length < 2) return undefined;
    const idx = alive.indexOf(this.id);
    return alive[(idx + 1) % alive.length];
  }

  // ---- 消息处理 ----

  handle(msg: Message, tick: number): void {
    this.currentTick = tick;
    this.clock.witness(msg.clock);
    switch (msg.kind) {
      case 'PING':
        this.onPing(msg);
        break;
      case 'ACK':
        this.onAck(msg);
        break;
      case 'PING_REQ':
        this.onPingReq(msg);
        break;
      case 'REQ_ACK':
        this.onReqAck(msg);
        break;
      default:
        this.merge.handle(msg, tick);
    }
  }

  onTick(tick: number): void {
    this.currentTick = tick;
    this.merge.onTick(tick);

    for (const [seq, pending] of this.pendingPings) {
      if (tick - pending.sentTick >= ACK_TIMEOUT) {
        this.pendingPings.delete(seq);
        this.startIndirectProbe(pending.target);
      }
    }
    for (const [seq, pending] of this.indirectPending) {
      if (tick - pending.sentTick >= ACK_TIMEOUT) {
        this.indirectPending.delete(seq);
        this.sendReqAck(pending.origin, pending.oseq, pending.target, false);
      }
    }
    for (const [seq, req] of this.pendingReqs) {
      if (tick - req.sentTick >= REQ_TIMEOUT) {
        this.pendingReqs.delete(seq);
        if (req.kind === 'probe') this.registerProbeResult(req.target, false);
      }
    }
    for (const member of this.membership.all()) {
      if (member.status === 'suspect' && tick - (this.suspectSince.get(member.id) ?? tick) >= SUSPECT_TIMEOUT) {
        this.membership.mark(member.id, 'dead');
        this.suspectSince.delete(member.id);
        this.log('member-dead', `node=${member.id}`);
      }
    }

    if ((tick + this.id) % PROBE_INTERVAL === 0) this.probeSuccessor();
    if ((tick + this.id * 3) % REATTACH_INTERVAL === 0) this.reattach();
    this.noteDomainChange();
  }

  private onPing(msg: PingMsg): void {
    this.checkForeign(msg.domain, msg.from, msg.via ? 'PING_REQ' : 'PING');
    this.maybeGossip(msg.from, msg.digest);
    this.send({
      kind: 'ACK',
      from: this.id,
      to: msg.from,
      seq: msg.seq,
      via: msg.via,
      domain: this.partitionInfo(),
      clock: this.clock.tick(),
      digest: this.membership.all(),
    });
  }

  private onAck(msg: AckMsg): void {
    if (msg.via) {
      // 我是中介：目标已应答，向发起方回报 PingReq 成功
      const pending = this.indirectPending.get(msg.seq);
      if (pending) {
        this.indirectPending.delete(msg.seq);
        this.sendReqAck(pending.origin, pending.oseq, msg.from, true, msg.domain);
      }
    } else {
      this.pendingPings.delete(msg.seq);
    }
    this.checkForeign(msg.domain, msg.from, msg.via ? 'PING_REQ' : 'PING');
    this.maybeGossip(msg.from, msg.digest);
  }

  private onPingReq(msg: PingReqMsg): void {
    this.checkForeign(msg.domain, msg.from, 'PING_REQ');
    this.maybeGossip(msg.from, msg.digest);
    const seq = ++this.seq;
    this.indirectPending.set(seq, { origin: msg.from, oseq: msg.seq, target: msg.target, sentTick: this.currentTick });
    this.send({
      kind: 'PING',
      from: this.id,
      to: msg.target,
      seq,
      via: { origin: msg.from, oseq: msg.seq },
      domain: this.partitionInfo(),
      clock: this.clock.tick(),
      digest: this.membership.all(),
    });
  }

  private onReqAck(msg: ReqAckMsg): void {
    const req = this.pendingReqs.get(msg.seq);
    if (req) {
      this.pendingReqs.delete(msg.seq);
      if (req.kind === 'probe') {
        this.registerProbeResult(req.target, msg.ok);
      } else if (msg.ok && msg.targetDomain) {
        // 重接探测成功：首个跨子网 PingReq 打通 -> 触发合并握手
        this.checkForeign(msg.targetDomain, req.target, 'PING_REQ');
      }
    }
    this.maybeGossip(msg.from, msg.digest);
  }

  // ---- 探测与重接 ----

  private probeSuccessor(): void {
    const successor = this.ringSuccessor();
    if (successor === undefined) return;
    for (const pending of this.pendingPings.values()) {
      if (pending.target === successor) return;
    }
    const seq = ++this.seq;
    this.pendingPings.set(seq, { target: successor, sentTick: this.currentTick });
    this.send({
      kind: 'PING',
      from: this.id,
      to: successor,
      seq,
      domain: this.partitionInfo(),
      clock: this.clock.tick(),
      digest: this.membership.all(),
    });
  }

  private startIndirectProbe(target: number): void {
    const others = this.membership
      .aliveIds()
      .filter((id) => id !== this.id && id !== target)
      .slice(0, INDIRECT_K);
    if (others.length === 0) {
      this.suspect(target);
      return;
    }
    this.probeTrack.set(target, others.length);
    for (const via of others) {
      const seq = ++this.seq;
      this.pendingReqs.set(seq, { target, kind: 'probe', sentTick: this.currentTick });
      this.send({
        kind: 'PING_REQ',
        from: this.id,
        to: via,
        seq,
        target,
        domain: this.partitionInfo(),
        clock: this.clock.tick(),
        digest: this.membership.all(),
      });
    }
  }

  private registerProbeResult(target: number, ok: boolean): void {
    const outstanding = this.probeTrack.get(target);
    if (outstanding === undefined) return;
    if (ok) {
      this.probeTrack.delete(target);
      return;
    }
    if (outstanding <= 1) {
      this.probeTrack.delete(target);
      this.suspect(target);
    } else {
      this.probeTrack.set(target, outstanding - 1);
    }
  }

  /** 对死亡节点的重接探测：经环后继中介发送 PingReq。 */
  private reattach(): void {
    if (this.merge.state !== 'idle') return;
    const dead = this.membership.deadIds();
    if (dead.length === 0) return;
    const intermediary = this.ringSuccessor();
    if (intermediary === undefined) return;
    const target = dead[0];
    for (const req of this.pendingReqs.values()) {
      if (req.target === target) return;
    }
    const seq = ++this.seq;
    this.pendingReqs.set(seq, { target, kind: 'reattach', sentTick: this.currentTick });
    this.send({
      kind: 'PING_REQ',
      from: this.id,
      to: intermediary,
      seq,
      target,
      domain: this.partitionInfo(),
      clock: this.clock.tick(),
      digest: this.membership.all(),
    });
  }

  private suspect(target: number): void {
    if (this.membership.get(target).status !== 'alive') return;
    this.membership.mark(target, 'suspect');
    this.suspectSince.set(target, this.currentTick);
    this.log('member-suspect', `node=${target}`);
  }

  private sendReqAck(to: number, oseq: number, target: number, ok: boolean, targetDomain?: DomainInfo): void {
    this.send({
      kind: 'REQ_ACK',
      from: this.id,
      to,
      seq: oseq,
      target,
      ok,
      targetDomain,
      domain: this.partitionInfo(),
      clock: this.clock.tick(),
      digest: this.membership.all(),
    });
  }

  // ---- 域检测与 gossip ----

  private checkForeign(domain: DomainInfo, peerId: number, via: string): void {
    if (this.isForeignContact(domain, peerId)) {
      this.merge.trigger(domain, peerId, this.currentTick, via);
    }
  }

  /**
   * 只采纳“发送方在我的存活集合中”的摘要（同一连通分量判据）：
   * 子网内部收敛期间域标识处于过渡态，gossip 必须流通；
   * 异域节点必然不在我的存活集合中，其视图只能经合并握手安装。
   */
  private maybeGossip(from: number, digest: MemberState[]): void {
    if (this.membership.get(from).status !== 'alive') return;
    const { refuted } = this.membership.applyDigest(digest, this.id);
    if (refuted) this.log('refuted', 'self marked non-alive, incarnation bumped');
  }

  private noteDomainChange(): void {
    const current = this.partitionInfo().partitionId;
    if (current !== this.lastPartitionId) {
      this.log('domain-change', `${this.lastPartitionId} -> ${current}`);
      this.lastPartitionId = current;
    }
  }

  private log(event: string, detail: string): void {
    this.logger(this.id, event, detail, this.currentTick);
  }
}
