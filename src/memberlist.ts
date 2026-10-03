import type {
  DigestEntry,
  HealthStatus,
  MemberNode,
  NodeMetadata,
  VersionedMetadata,
} from './types.ts';

/**
 * 带标签与元数据属性的异构节点成员列表。
 *
 * - 每个节点携带动态元数据（zone / weight / role / labels）。
 * - 元数据并发更新通过 (version, updatedBy) 做 Last-Writer-Wins 仲裁，
 *   版本号采用 Lamport 时钟：本地更新时取 max(已知版本)+1。
 * - 支持 Gossip 合并：digest 交换 + 全量 merge，收敛到一致视图。
 */
export class MemberList {
  readonly localId: string;
  private nodes = new Map<string, MemberNode>();
  private clock = 0;

  constructor(localId: string) {
    this.localId = localId;
  }

  /** 加入（或注册）一个节点 */
  addNode(id: string, address: string, meta: NodeMetadata): MemberNode {
    const existing = this.nodes.get(id);
    if (existing) return existing;
    const node: MemberNode = {
      id,
      address,
      health: 'healthy',
      healthVersion: 1,
      record: {
        meta: structuredClone(meta),
        version: this.tick(),
        updatedBy: this.localId,
        updatedAt: Date.now(),
      },
    };
    this.nodes.set(id, node);
    return node;
  }

  removeNode(id: string): boolean {
    return this.nodes.delete(id);
  }

  get(id: string): MemberNode | undefined {
    return this.nodes.get(id);
  }

  all(): MemberNode[] {
    return [...this.nodes.values()];
  }

  /**
   * 本地更新某节点元数据（并发安全：版本号仲裁）。
   * 返回新版本号。
   */
  updateMetadata(id: string, patch: Partial<NodeMetadata>): number {
    const node = this.nodes.get(id);
    if (!node) throw new Error(`unknown node: ${id}`);
    const next: NodeMetadata = {
      ...node.record.meta,
      ...patch,
      labels: { ...node.record.meta.labels, ...(patch.labels ?? {}) },
    };
    const version = this.tick(node.record.version);
    node.record = {
      meta: next,
      version,
      updatedBy: this.localId,
      updatedAt: Date.now(),
    };
    return version;
  }

  /** 本地标记健康状态 */
  setHealth(id: string, health: HealthStatus): void {
    const node = this.nodes.get(id);
    if (!node) throw new Error(`unknown node: ${id}`);
    node.health = health;
    node.healthVersion = this.tick(node.healthVersion);
  }

  /**
   * 应用一条来自远端的元数据记录（Gossip 接收路径）。
   * 仲裁规则：version 大者胜；version 相同则 updatedBy 字典序大者胜（确定性收敛）。
   */
  applyRemote(id: string, address: string, record: VersionedMetadata): boolean {
    this.observe(record.version);
    const node = this.nodes.get(id);
    if (!node) {
      this.nodes.set(id, {
        id,
        address,
        health: 'healthy',
        healthVersion: 1,
        record: structuredClone(record),
      });
      return true;
    }
    if (this.wins(record, node.record)) {
      node.record = structuredClone(record);
      node.address = address;
      return true;
    }
    return false;
  }

  /** 应用远端健康状态 */
  applyRemoteHealth(id: string, health: HealthStatus, healthVersion: number): void {
    this.observe(healthVersion);
    const node = this.nodes.get(id);
    if (node && healthVersion > node.healthVersion) {
      node.health = health;
      node.healthVersion = healthVersion;
    }
  }

  /** 生成 Gossip 摘要（版本向量） */
  digest(): DigestEntry[] {
    return this.all().map((n) => ({
      id: n.id,
      metaVersion: n.record.version,
      healthVersion: n.healthVersion,
    }));
  }

  /**
   * 与对端做一次 Gossip 往返：
   * 1. 交换 digest，找出对方缺失/过期的节点；
   * 2. 双向推送差异记录并合并。
   */
  gossipWith(peer: MemberList): void {
    const mine = this.digest();
    const theirs = peer.digest();

    for (const entry of theirs) {
      const local = this.nodes.get(entry.id);
      const remoteNode = peer.get(entry.id)!;
      if (!local || entry.metaVersion >= local.record.version) {
        // 同版本时由 applyRemote 内部的 (version, updatedBy) 仲裁决定胜负
        this.applyRemote(entry.id, remoteNode.address, remoteNode.record);
      }
      if (local && entry.healthVersion > local.healthVersion) {
        this.applyRemoteHealth(entry.id, remoteNode.health, entry.healthVersion);
      }
    }
    for (const entry of mine) {
      const remote = peer.nodes.get(entry.id);
      const localNode = this.nodes.get(entry.id)!;
      if (!remote || entry.metaVersion >= remote.record.version) {
        peer.applyRemote(entry.id, localNode.address, localNode.record);
      }
      if (remote && entry.healthVersion > remote.healthVersion) {
        peer.applyRemoteHealth(entry.id, localNode.health, entry.healthVersion);
      }
    }
  }

  private wins(a: VersionedMetadata, b: VersionedMetadata): boolean {
    if (a.version !== b.version) return a.version > b.version;
    return a.updatedBy > b.updatedBy;
  }

  private tick(observed = 0): number {
    this.clock = Math.max(this.clock, observed) + 1;
    return this.clock;
  }

  private observe(version: number): void {
    this.clock = Math.max(this.clock, version);
  }
}
