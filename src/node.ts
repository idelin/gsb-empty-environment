import {
  Lsa,
  Message,
  NodeId,
  Version,
  VersionVector,
  clone,
  compareVersions,
} from "./types";

/**
 * 路由器节点：维护 LSDB，负责自源 LSA 的生成/撤销、
 * 泛洪、摘要交换（summary）与缺失补齐（request/update）。
 */
export class Node {
  readonly id: NodeId;
  private readonly lsdb = new Map<NodeId, Lsa>();
  private readonly peers = new Map<NodeId, number>(); // 当前 up 的邻居 -> 开销
  private outbox: Message[] = [];
  readonly stats = { installed: 0, skippedStale: 0 };

  constructor(id: NodeId) {
    this.id = id;
  }

  // ---- 链路管理（由模拟器驱动） ----

  setPeer(peer: NodeId, cost: number): void {
    this.peers.set(peer, cost);
  }

  removePeer(peer: NodeId): void {
    this.peers.delete(peer);
  }

  /** 依据当前 up 链路重新生成自源 LSA */
  refreshSelfLsa(): Lsa {
    const links: Record<NodeId, number> = {};
    for (const [peer, cost] of [...this.peers.entries()].sort()) {
      links[peer] = cost;
    }
    return this.originate(links);
  }

  // ---- 自源 LSA 生命周期 ----

  originate(links: Record<NodeId, number>): Lsa {
    const cur = this.lsdb.get(this.id);
    let version: Version;
    if (!cur) {
      version = { generation: 0, sequence: 0 };
    } else if (cur.withdrawn) {
      // 删除后重建必须使用新代次：代次 +1、序号归零。
      version = { generation: cur.version.generation + 1, sequence: 0 };
    } else {
      version = { generation: cur.version.generation, sequence: cur.version.sequence + 1 };
    }
    const lsa: Lsa = { origin: this.id, version, links: { ...links }, withdrawn: false };
    this.install(lsa);
    this.flood(lsa, null);
    return lsa;
  }

  /** 撤销（删除）自源 LSA：同代次序号 +1，标记 withdrawn 并泛洪 */
  withdraw(): Lsa {
    const cur = this.lsdb.get(this.id);
    const version: Version = cur
      ? { generation: cur.version.generation, sequence: cur.version.sequence + 1 }
      : { generation: 0, sequence: 0 };
    const lsa: Lsa = { origin: this.id, version, links: {}, withdrawn: true };
    this.install(lsa);
    this.flood(lsa, null);
    return lsa;
  }

  // ---- 摘要交换与缺失补齐 ----

  /** 主动发出摘要：逐来源版本向量 */
  sendSummaryTo(peer: NodeId): void {
    this.outbox.push({ kind: "summary", from: this.id, to: peer, vector: this.versionVector() });
  }

  versionVector(): VersionVector {
    const vector: VersionVector = {};
    for (const [origin, lsa] of [...this.lsdb.entries()].sort()) {
      vector[origin] = { ...lsa.version };
    }
    return vector;
  }

  handleSummary(peer: NodeId, vector: VersionVector): void {
    const want: NodeId[] = [];
    const offer: Lsa[] = [];
    const origins = new Set<NodeId>([...Object.keys(vector), ...this.lsdb.keys()]);
    for (const origin of [...origins].sort()) {
      const mine = this.lsdb.get(origin);
      const theirs = vector[origin];
      // 对方更新或我缺失 -> 精确请求该来源（而非按全局最大版本拉取一片）
      if (theirs && (!mine || compareVersions(theirs, mine.version) > 0)) want.push(origin);
      // 我更新或对方缺失 -> 主动补齐给对方
      if (mine && (!theirs || compareVersions(mine.version, theirs) > 0)) offer.push(clone(mine));
    }
    if (want.length > 0) {
      this.outbox.push({ kind: "request", from: this.id, to: peer, origins: want });
    }
    if (offer.length > 0) {
      this.outbox.push({ kind: "update", from: this.id, to: peer, lsas: offer });
    }
  }

  handleRequest(peer: NodeId, origins: NodeId[]): void {
    const lsas: Lsa[] = [];
    for (const origin of origins) {
      const lsa = this.lsdb.get(origin);
      if (lsa) lsas.push(clone(lsa));
    }
    if (lsas.length > 0) {
      this.outbox.push({ kind: "update", from: this.id, to: peer, lsas });
    }
  }

  handleUpdate(from: NodeId, lsas: Lsa[]): void {
    for (const raw of lsas) {
      const cur = this.lsdb.get(raw.origin);
      // 跳过被同源新版本覆盖的旧更新：仅接受严格更新的 (代次, 序号)
      if (cur && compareVersions(raw.version, cur.version) <= 0) {
        this.stats.skippedStale++;
        continue;
      }
      const lsa = clone(raw);
      this.install(lsa);
      this.flood(lsa, from);
    }
  }

  // ---- 内部 ----

  private install(lsa: Lsa): void {
    this.lsdb.set(lsa.origin, clone(lsa));
    this.stats.installed++;
  }

  private flood(lsa: Lsa, except: NodeId | null): void {
    for (const peer of this.peers.keys()) {
      if (peer === except) continue;
      this.outbox.push({ kind: "update", from: this.id, to: peer, lsas: [clone(lsa)] });
    }
  }

  // ---- 只读视图 / 队列 ----

  getLsa(origin: NodeId): Lsa | undefined {
    const lsa = this.lsdb.get(origin);
    return lsa ? clone(lsa) : undefined;
  }

  lsdbEntries(): [NodeId, Lsa][] {
    return [...this.lsdb.entries()];
  }

  drainOutbox(): Message[] {
    const out = this.outbox;
    this.outbox = [];
    return out;
  }
}
