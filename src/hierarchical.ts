/**
 * 分层（Sub-cluster）Gossip 节点。
 *
 * 拓扑优化策略：
 * - Cell 内：高频 gossip（每 intraIntervalMs 向 intraFanout 个同 Cell 随机
 *   对等体推送心跳摘要），心跳在 Cell 内以流行病速度扩散，因此本地故障
 *   可以在亚秒级被感知。
 * - 跨 Cell：低频汇聚广播。每个 Cell 的 delegate（id 最小的若干存活节点）
 *   每 interIntervalMs 把本 Cell 的聚合心跳摘要发给其他每个 Cell 的
 *   delegate 候选；跨 Cell 信息再借助 Cell 内 gossip 的 remote 增量扩散。
 *
 * 单节点包量从扁平广播的 O(N) 降到 O(fanout) + 摊销的 O(cells/cellSize)。
 */

import type { Endpoint, Network, Simulator } from './sim.ts';
import type { CellAssignment } from './membership.ts';
import type { DigestMessage, HeartbeatEntry } from './protocol.ts';

export interface HierarchicalConfig {
  /** Cell 内 gossip 周期（高频） */
  intraIntervalMs: number;
  /** 每周期向多少个同 Cell 对等体推送 */
  intraFanout: number;
  /** 心跳停滞多久判定为疑似故障 */
  suspectTimeoutMs: number;
  /** 跨 Cell 汇聚广播周期（低频） */
  interIntervalMs: number;
  /** 每个 Cell 的 delegate 数量（冗余，防单点） */
  delegateCount: number;
  /** 跨 Cell 心跳在 Cell 内继续扩散的保留窗口 */
  remoteRetentionMs: number;
  /** 每个 intra 摘要最多携带多少条跨 Cell 心跳增量（有界 piggyback） */
  maxRemotePerDigest: number;
}

export const DEFAULT_HIERARCHICAL_CONFIG: HierarchicalConfig = {
  intraIntervalMs: 25,
  intraFanout: 3,
  suspectTimeoutMs: 150,
  interIntervalMs: 1000,
  delegateCount: 2,
  remoteRetentionMs: 3000,
  maxRemotePerDigest: 16,
};

export interface HierarchicalNodeOptions {
  id: number;
  nodeCount: number;
  sim: Simulator;
  net: Network;
  config: HierarchicalConfig;
  assignment: CellAssignment;
  cells: number[][];
  nodes: HierarchicalNode[];
  rand: () => number;
}

export class HierarchicalNode implements Endpoint {
  readonly id: number;
  alive = true;
  crashedAt = -1;

  private sim: Simulator;
  private net: Network;
  private cfg: HierarchicalConfig;
  private rand: () => number;
  private nodes: HierarchicalNode[];
  private assignment: CellAssignment;

  readonly cellId: number;
  private myCell: number[];
  private peers: number[];
  private cells: number[][];

  private heartbeat = 0;
  private counters: Float64Array;
  private updatedAt: Float64Array;
  private suspected: Uint8Array;
  /** 每个对等体被本节点判定疑似故障的虚拟时间，-1 表示未怀疑（供指标采集）。 */
  readonly suspectAt: Float64Array;

  private data = new Map<string, number>();
  readonly dataLearnedAt = new Map<string, number>();
  /** 跨 Cell 心跳增量的轮转队列：有界扩散，避免每个摘要都全量扫描 O(N)。 */
  private remoteIds: number[] = [];
  private remoteIndexOf: Float64Array;
  private remoteChangedAt: Float64Array;
  private remoteCursor = 0;

  constructor(opts: HierarchicalNodeOptions) {
    this.id = opts.id;
    this.sim = opts.sim;
    this.net = opts.net;
    this.cfg = opts.config;
    this.rand = opts.rand;
    this.nodes = opts.nodes;
    this.assignment = opts.assignment;
    this.cells = opts.cells;
    this.cellId = opts.assignment.cellOf(opts.id);
    this.myCell = this.cells[this.cellId];
    this.peers = this.myCell.filter((m) => m !== this.id);
    const n = opts.nodeCount;
    this.counters = new Float64Array(n);
    this.updatedAt = new Float64Array(n);
    this.suspected = new Uint8Array(n);
    this.suspectAt = new Float64Array(n).fill(-1);
    this.remoteIndexOf = new Float64Array(n).fill(-1);
    this.remoteChangedAt = new Float64Array(n);
  }

  start(): void {
    // 初始随机相位，避免全网同步齐发造成瞬时尖峰
    this.sim.schedule(this.rand() * this.cfg.intraIntervalMs, () => this.intraTick());
    this.sim.schedule(this.rand() * this.cfg.interIntervalMs, () => this.interTick());
  }

  crash(): void {
    this.alive = false;
    this.crashedAt = this.sim.now;
  }

  broadcastData(key: string, version: number): void {
    this.learnData(key, version);
  }

  dataVersion(key: string): number | undefined {
    return this.data.get(key);
  }

  isSuspected(peer: number): boolean {
    return this.suspected[peer] === 1;
  }

  private intraTick(): void {
    if (!this.alive) return;
    this.detectFailures();
    this.heartbeat++;
    this.counters[this.id] = this.heartbeat;
    this.updatedAt[this.id] = this.sim.now;
    const digest = this.buildDigest('intra');
    for (let i = 0; i < this.cfg.intraFanout; i++) {
      const target = this.peers[Math.floor(this.rand() * this.peers.length)];
      this.net.send(this, this.nodes[target], digest);
    }
    this.sim.schedule(this.cfg.intraIntervalMs, () => this.intraTick());
  }

  private interTick(): void {
    if (!this.alive) return;
    if (this.isDelegate()) {
      const digest = this.buildDigest('inter');
      for (let c = 0; c < this.cells.length; c++) {
        if (c === this.cellId) continue;
        const candidates = this.cells[c];
        const limit = Math.min(this.cfg.delegateCount, candidates.length);
        for (let i = 0; i < limit; i++) {
          this.net.send(this, this.nodes[candidates[i]], digest);
        }
      }
    }
    this.sim.schedule(this.cfg.interIntervalMs, () => this.interTick());
  }

  /** 本节点是否认为自己是 delegate：Cell 内 id 最小的 delegateCount 个"存活"成员。 */
  private isDelegate(): boolean {
    let rank = 0;
    for (const member of this.myCell) {
      if (member === this.id) return rank < this.cfg.delegateCount;
      if (this.suspected[member] === 0) rank++;
    }
    return false;
  }

  private buildDigest(kind: 'intra' | 'inter'): DigestMessage {
    const counters: number[] = [];
    for (const member of this.myCell) {
      counters.push(this.counters[member]);
    }
    const remoteIds: number[] = [];
    const remoteCounters: number[] = [];
    if (kind === 'intra') {
      const now = this.sim.now;
      // 轮转扫描，最多挑 maxRemotePerDigest 条仍处保留窗口内的跨 Cell 增量
      const total = this.remoteIds.length;
      let scanned = 0;
      while (
        remoteIds.length < this.cfg.maxRemotePerDigest &&
        scanned < total
      ) {
        const id = this.remoteIds[this.remoteCursor];
        this.remoteCursor = (this.remoteCursor + 1) % total;
        scanned++;
        if (now - this.remoteChangedAt[id] <= this.cfg.remoteRetentionMs) {
          remoteIds.push(id);
          remoteCounters.push(this.counters[id]);
        }
      }
    }
    return {
      kind,
      from: this.id,
      members: this.myCell,
      counters,
      remoteIds,
      remoteCounters,
      data: [...this.data],
    };
  }

  receive(msg: unknown): void {
    const digest = msg as DigestMessage;
    for (let i = 0; i < digest.members.length; i++) {
      this.learn(digest.members[i], digest.counters[i]);
    }
    for (let i = 0; i < digest.remoteIds.length; i++) {
      this.learn(digest.remoteIds[i], digest.remoteCounters[i]);
    }
    for (const [key, version] of digest.data) this.learnData(key, version);
  }

  private learn(id: number, counter: number): void {
    if (!Number.isFinite(counter)) return; // 防御畸形摘要，避免污染本地状态
    if (counter <= this.counters[id]) return;
    this.counters[id] = counter;
    this.updatedAt[id] = this.sim.now;
    if (this.assignment.cellOf(id) !== this.cellId) {
      if (this.remoteIndexOf[id] === -1) {
        this.remoteIndexOf[id] = this.remoteIds.length;
        this.remoteIds.push(id);
      }
      this.remoteChangedAt[id] = this.sim.now;
    }
    if (this.suspected[id] === 1) {
      // 心跳重新推进，撤销怀疑
      this.suspected[id] = 0;
      this.suspectAt[id] = -1;
    }
  }

  private learnData(key: string, version: number): void {
    const current = this.data.get(key);
    if (current !== undefined && current >= version) return;
    this.data.set(key, version);
    if (!this.dataLearnedAt.has(key)) {
      this.dataLearnedAt.set(key, this.sim.now);
    }
  }

  /** 故障检测只作用于本 Cell（局部 SLO）；跨 Cell 健康度由汇聚摘要的时效性体现。 */
  private detectFailures(): void {
    const now = this.sim.now;
    for (const peer of this.peers) {
      if (this.suspected[peer] === 1) continue;
      if (now - this.updatedAt[peer] > this.cfg.suspectTimeoutMs) {
        this.suspected[peer] = 1;
        this.suspectAt[peer] = now;
      }
    }
  }
}
