import type { Clock } from './clock.ts';
import type { Message, NodeId } from './messages.ts';
import type { Transport } from './transport.ts';

export type MemberStatus = 'alive' | 'down';

export type SwimEvent =
  | { type: 'period-start'; node: NodeId; seq: number; target: NodeId }
  | { type: 'ping'; node: NodeId; seq: number; target: NodeId }
  | { type: 'direct-timeout'; node: NodeId; seq: number; target: NodeId }
  | { type: 'ping-req'; node: NodeId; seq: number; target: NodeId; proxies: NodeId[] }
  | { type: 'ack'; node: NodeId; seq: number; target: NodeId; via: NodeId }
  | { type: 'probe-success'; node: NodeId; seq: number; target: NodeId }
  | { type: 'probe-fail'; node: NodeId; seq: number; target: NodeId }
  | { type: 'mark-down'; node: NodeId; target: NodeId };

export interface SwimOptions {
  id: NodeId;
  peers: NodeId[];
  transport: Transport;
  clock: Clock;
  /** 协议周期时长，默认 1000ms。 */
  periodMs?: number;
  /** 直连 Ping 超时，默认 300ms。 */
  pingTimeoutMs?: number;
  /** 间接探测（PingReq）超时，默认 500ms。需满足 pingTimeoutMs + indirectTimeoutMs < periodMs。 */
  indirectTimeoutMs?: number;
  /** 间接代理节点数量 K，默认 3。 */
  k?: number;
  /** 随机源（注入以便测试确定性），默认 Math.random。 */
  random?: () => number;
  onEvent?: (event: SwimEvent) => void;
}

interface PendingProbe {
  seq: number;
  target: NodeId;
  done: boolean;
  directTimer: unknown;
  indirectTimer: unknown;
}

interface ProxyEntry {
  requesters: NodeId[];
  timer: unknown;
}

/**
 * SWIM 风格故障检测器：
 * 每个协议周期随机选择一个存活成员发送 Ping；
 * 直连 Ack 超时后，随机选 K 个代理节点发送 PingReq 协助探测；
 * 周期内任一路径收到 Ack 即判定存活，否则标记 Down。
 */
export class SwimNode {
  readonly id: NodeId;

  private readonly clock: Clock;
  private readonly transport: Transport;
  private readonly periodMs: number;
  private readonly pingTimeoutMs: number;
  private readonly indirectTimeoutMs: number;
  private readonly k: number;
  private readonly random: () => number;
  private readonly emit: (event: SwimEvent) => void;

  private readonly members = new Map<NodeId, MemberStatus>();
  private seq = 0;
  private periodTimer: unknown = null;
  private pending: PendingProbe | null = null;
  /** 作为代理时待转发的请求，键为 `seq:target`。 */
  private readonly proxied = new Map<string, ProxyEntry>();

  constructor(options: SwimOptions) {
    this.id = options.id;
    this.clock = options.clock;
    this.transport = options.transport;
    this.periodMs = options.periodMs ?? 1000;
    this.pingTimeoutMs = options.pingTimeoutMs ?? 300;
    this.indirectTimeoutMs = options.indirectTimeoutMs ?? 500;
    this.k = options.k ?? 3;
    this.random = options.random ?? Math.random;
    this.emit = options.onEvent ?? (() => {});
    for (const peer of options.peers) {
      if (peer !== this.id) this.members.set(peer, 'alive');
    }
    this.transport.onMessage((message) => this.handleMessage(message));
  }

  start(): void {
    if (this.periodTimer !== null) return;
    this.periodTimer = this.clock.setTimeout(() => this.runPeriod(), this.periodMs);
  }

  stop(): void {
    if (this.periodTimer !== null) this.clock.clearTimeout(this.periodTimer);
    this.periodTimer = null;
    if (this.pending) this.finishProbe(false);
  }

  statusOf(id: NodeId): MemberStatus | undefined {
    return this.members.get(id);
  }

  aliveMembers(): NodeId[] {
    const alive: NodeId[] = [];
    for (const [id, status] of this.members) {
      if (status === 'alive') alive.push(id);
    }
    return alive;
  }

  private runPeriod(): void {
    this.periodTimer = this.clock.setTimeout(() => this.runPeriod(), this.periodMs);
    if (this.pending) this.finishProbe(false);
    const target = this.pickTarget();
    if (target === null) return;
    const seq = ++this.seq;
    const pending: PendingProbe = {
      seq,
      target,
      done: false,
      directTimer: null,
      indirectTimer: null,
    };
    this.pending = pending;
    this.emit({ type: 'period-start', node: this.id, seq, target });
    this.emit({ type: 'ping', node: this.id, seq, target });
    this.transport.send(target, { type: 'ping', seq, from: this.id, to: target });
    pending.directTimer = this.clock.setTimeout(() => this.onDirectTimeout(pending), this.pingTimeoutMs);
  }

  private onDirectTimeout(pending: PendingProbe): void {
    if (this.pending !== pending || pending.done) return;
    this.emit({ type: 'direct-timeout', node: this.id, seq: pending.seq, target: pending.target });
    const proxies = this.pickProxies(pending.target);
    if (proxies.length > 0) {
      this.emit({ type: 'ping-req', node: this.id, seq: pending.seq, target: pending.target, proxies });
    }
    for (const proxy of proxies) {
      this.transport.send(proxy, { type: 'ping-req', seq: pending.seq, from: this.id, to: proxy, target: pending.target });
    }
    pending.indirectTimer = this.clock.setTimeout(() => this.onIndirectTimeout(pending), this.indirectTimeoutMs);
  }

  private onIndirectTimeout(pending: PendingProbe): void {
    if (this.pending !== pending || pending.done) return;
    this.finishProbe(false);
  }

  private finishProbe(success: boolean): void {
    const pending = this.pending;
    if (!pending || pending.done) return;
    pending.done = true;
    if (pending.directTimer !== null) this.clock.clearTimeout(pending.directTimer);
    if (pending.indirectTimer !== null) this.clock.clearTimeout(pending.indirectTimer);
    this.pending = null;
    this.emit({ type: success ? 'probe-success' : 'probe-fail', node: this.id, seq: pending.seq, target: pending.target });
    if (!success) {
      this.members.set(pending.target, 'down');
      this.emit({ type: 'mark-down', node: this.id, target: pending.target });
    }
  }

  private handleMessage(message: Message): void {
    switch (message.type) {
      case 'ping':
        this.onPing(message);
        break;
      case 'ack':
        this.onAck(message);
        break;
      case 'ping-req':
        this.onPingReq(message);
        break;
    }
  }

  private onPing(message: Extract<Message, { type: 'ping' }>): void {
    // 无论来自探测者还是代理，都直接应答；target 填自己，表示“被探测节点是我”。
    this.transport.send(message.from, { type: 'ack', seq: message.seq, from: this.id, to: message.from, target: this.id });
  }

  private onAck(message: Extract<Message, { type: 'ack' }>): void {
    // 情况 1：本节点正在进行的探测（直连 Ack 或代理转发的 Ack）。
    const pending = this.pending;
    if (pending && !pending.done && message.seq === pending.seq && message.target === pending.target) {
      this.emit({ type: 'ack', node: this.id, seq: message.seq, target: message.target, via: message.from });
      this.finishProbe(true);
      return;
    }
    // 情况 2：本节点是代理，这是被代理目标回来的 Ack，需要转发给请求者。
    const key = `${message.seq}:${message.from}`;
    const entry = this.proxied.get(key);
    if (entry) {
      this.proxied.delete(key);
      this.clock.clearTimeout(entry.timer);
      for (const requester of entry.requesters) {
        this.transport.send(requester, { type: 'ack', seq: message.seq, from: this.id, to: requester, target: message.from });
      }
    }
  }

  private onPingReq(message: Extract<Message, { type: 'ping-req' }>): void {
    if (message.target === this.id) {
      this.transport.send(message.from, { type: 'ack', seq: message.seq, from: this.id, to: message.from, target: this.id });
      return;
    }
    if (this.members.get(message.target) === 'down') return;
    // 作为代理：代替请求者 Ping 目标，等待 Ack 后转发。
    const key = `${message.seq}:${message.target}`;
    let entry = this.proxied.get(key);
    if (!entry) {
      const timer = this.clock.setTimeout(() => this.proxied.delete(key), this.pingTimeoutMs);
      entry = { requesters: [], timer };
      this.proxied.set(key, entry);
      this.transport.send(message.target, { type: 'ping', seq: message.seq, from: this.id, to: message.target });
    }
    entry.requesters.push(message.from);
  }

  private pickTarget(): NodeId | null {
    const candidates = this.aliveMembers();
    if (candidates.length === 0) return null;
    return candidates[Math.floor(this.random() * candidates.length)];
  }

  private pickProxies(exclude: NodeId): NodeId[] {
    const pool = this.aliveMembers().filter((id) => id !== exclude);
    const chosen: NodeId[] = [];
    while (chosen.length < this.k && pool.length > 0) {
      const index = Math.floor(this.random() * pool.length);
      chosen.push(pool.splice(index, 1)[0]);
    }
    return chosen;
  }
}
