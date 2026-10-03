import { EventEmitter } from 'node:events';
import { randomUUID } from 'node:crypto';
import type { BootstrapAck, Message, NodeMeta, ShuffleReply } from './types.ts';
import type { Transport } from './transport.ts';
import { ShuffleRing } from './shuffle-ring.ts';

export interface ClusterNodeOptions {
  /** 节点 ID，缺省自动生成 */
  id?: string;
  /** 本节点在传输层上的地址 */
  address: string;
  transport: Transport;
  /** 静态 Seed 地址列表（冷启动时按序/轮询尝试） */
  seeds?: string[];
  /** 是否作为 Seed 启动（新集群组网时至少一个） */
  isSeed?: boolean;
  /** Gossip 洗牌间隔，默认 100ms */
  gossipIntervalMs?: number;
  /** 每次洗牌交换的成员样本数，默认 10 */
  gossipSampleSize?: number;
  /** Bootstrap 总超时，默认 3000ms；超时抛错（孤儿节点入网失败） */
  bootstrapTimeoutMs?: number;
  /** 两次 Seed 重试的间隔，默认 50ms */
  bootstrapRetryMs?: number;
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

/**
 * 新节点冷启动自发现握手器。
 *
 * 工作流程：
 * 1. start() 绑定传输层；Seed 节点直接成环（新集群组网）。
 * 2. 非 Seed 节点向静态 Seed 列表发起 BOOTSTRAP 握手，
 *    失败时轮询重试直到超时（孤儿节点入网）。
 * 3. 收到 BOOTSTRAP_ACK 后合并全网成员快照，同步洗牌环，
 *    立即启动 Gossip 循环，融入常规格网。
 */
export class ClusterNode extends EventEmitter {
  readonly meta: NodeMeta;
  private readonly transport: Transport;
  private readonly seeds: string[];
  private readonly gossipIntervalMs: number;
  private readonly gossipSampleSize: number;
  private readonly bootstrapTimeoutMs: number;
  private readonly bootstrapRetryMs: number;

  private readonly members = new Map<string, NodeMeta>();
  private readonly ring: ShuffleRing;
  private gossipTimer: NodeJS.Timeout | undefined;

  /** Bootstrap 握手是否已完成 */
  bootstrapped = false;

  constructor(options: ClusterNodeOptions) {
    super();
    this.meta = {
      id: options.id ?? randomUUID(),
      address: options.address,
      isSeed: options.isSeed ?? false,
      heartbeat: 0,
      joinedAt: Date.now(),
    };
    this.transport = options.transport;
    this.seeds = options.seeds ?? [];
    this.gossipIntervalMs = options.gossipIntervalMs ?? 100;
    this.gossipSampleSize = options.gossipSampleSize ?? 10;
    this.bootstrapTimeoutMs = options.bootstrapTimeoutMs ?? 3000;
    this.bootstrapRetryMs = options.bootstrapRetryMs ?? 50;
    this.ring = new ShuffleRing(this.meta.id);
  }

  /** 当前已知的全网成员表（含自身） */
  membership(): NodeMeta[] {
    return [...this.members.values()];
  }

  /** 洗牌环规模（不含自身） */
  get ringSize(): number {
    return this.ring.size;
  }

  /** Gossip 循环是否已启动 */
  get gossipActive(): boolean {
    return this.gossipTimer !== undefined;
  }

  async start(): Promise<void> {
    await this.transport.bind(this.meta.address, (msg) => this.onMessage(msg));
    this.members.set(this.meta.id, this.meta);

    if (this.meta.isSeed) {
      // 新集群组网：Seed 自身即初始成员，直接成环
      this.enterGrid();
    } else {
      await this.bootstrapHandshake();
    }
  }

  async stop(): Promise<void> {
    if (this.gossipTimer) {
      clearInterval(this.gossipTimer);
      this.gossipTimer = undefined;
    }
    this.transport.unbind(this.meta.address);
  }

  /** Bootstrap 握手：轮询 Seed 列表，拉取初始成员快照。 */
  private async bootstrapHandshake(): Promise<void> {
    const seeds = this.seeds.filter((s) => s !== this.meta.address);
    if (seeds.length === 0) {
      throw new Error(`node ${this.meta.id} is an orphan: no seed addresses configured`);
    }
    const deadline = Date.now() + this.bootstrapTimeoutMs;
    let attempt = 0;

    while (Date.now() < deadline) {
      const seedAddr = seeds[attempt % seeds.length];
      try {
        const reply = await this.transport.send(seedAddr, {
          type: 'BOOTSTRAP',
          from: this.meta,
        });
        if (reply && reply.type === 'BOOTSTRAP_ACK') {
          this.merge(reply.snapshot);
          this.enterGrid();
          return;
        }
      } catch {
        // Seed 暂不可达：换下一个 Seed 重试
      }
      attempt++;
      if (attempt % seeds.length === 0) {
        await sleep(Math.min(this.bootstrapRetryMs, Math.max(0, deadline - Date.now())));
      }
    }
    throw new Error(
      `node ${this.meta.id} bootstrap timed out after ${this.bootstrapTimeoutMs}ms (seeds: ${seeds.join(', ')})`,
    );
  }

  /** 快照合并完成，同步洗牌环并立即融入 Gossip 格网。 */
  private enterGrid(): void {
    this.bootstrapped = true;
    this.ring.sync(this.members.values());
    this.gossipTimer = setInterval(() => void this.gossipOnce(), this.gossipIntervalMs);
    this.gossipTimer.unref?.();
    this.emit('bootstrapped', this.membership());
  }

  /** 一轮 Gossip：从洗牌环取下一个节点，互换成員样本。 */
  private async gossipOnce(): Promise<void> {
    const peer = this.ring.next();
    if (!peer) return;
    this.meta.heartbeat++;
    try {
      const reply = await this.transport.send(peer.address, {
        type: 'SHUFFLE',
        from: this.meta,
        sample: this.sample(this.gossipSampleSize),
      });
      if (reply && reply.type === 'SHUFFLE_REPLY') {
        this.merge(reply.sample);
        this.emit('shuffle', peer);
      }
    } catch {
      // 轻量失败检测：对端不可达则移出成员表并重建环
      this.members.delete(peer.id);
      this.ring.sync(this.members.values());
      this.emit('peer:lost', peer);
    }
  }

  private onMessage(msg: Message): BootstrapAck | ShuffleReply | void {
    switch (msg.type) {
      case 'BOOTSTRAP': {
        this.merge([msg.from]);
        this.ring.sync(this.members.values());
        return { type: 'BOOTSTRAP_ACK', from: this.meta, snapshot: this.membership() };
      }
      case 'SHUFFLE': {
        this.merge(msg.sample);
        return { type: 'SHUFFLE_REPLY', from: this.meta, sample: this.sample(this.gossipSampleSize) };
      }
    }
  }

  /** 合并元数据：忽略自身，同 ID 时保留心跳更新的一方。 */
  private merge(metas: NodeMeta[]): void {
    for (const meta of metas) {
      if (meta.id === this.meta.id) continue;
      const existing = this.members.get(meta.id);
      if (!existing || existing.heartbeat < meta.heartbeat) {
        this.members.set(meta.id, { ...meta });
      }
    }
  }

  /** 从成员表随机抽取 k 个样本（不含自身）。 */
  private sample(k: number): NodeMeta[] {
    const pool = this.membership().filter((m) => m.id !== this.meta.id);
    for (let i = pool.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1));
      [pool[i], pool[j]] = [pool[j], pool[i]];
    }
    return pool.slice(0, k);
  }
}
