import type { NodeMetadata, NodeRecord, NodeState, WireRecord } from './types.ts';
import { metadataDigest, toWire } from './types.ts';

export interface MemberListOptions {
  /** 超过该毫秒数未收到心跳则标记为 suspect */
  suspectAfterMs?: number;
  /** 超过该毫秒数未收到心跳则标记为 dead（不参与路由） */
  deadAfterMs?: number;
  /** 注入时钟，便于测试 */
  now?: () => number;
}

/**
 * 异构节点成员列表：维护集群中所有节点的元数据视图。
 *
 * 并发仲裁规则（保证全网收敛到同一结果）：
 *  1. version 大者获胜（元数据版本号由属主节点单调递增）；
 *  2. version 相同（并发冲突）时，比较元数据规范化摘要，字典序大者获胜 —— 确定性、可收敛；
 *  3. heartbeat 取最大值；state 按 dead > suspect > alive 的严重程度传播。
 */
export class MemberList {
  readonly localNodeId: string;
  private readonly records = new Map<string, NodeRecord>();
  private readonly suspectAfterMs: number;
  private readonly deadAfterMs: number;
  private readonly now: () => number;

  constructor(localNodeId: string, address: string, metadata: NodeMetadata, options: MemberListOptions = {}) {
    this.localNodeId = localNodeId;
    this.suspectAfterMs = options.suspectAfterMs ?? 5_000;
    this.deadAfterMs = options.deadAfterMs ?? 30_000;
    this.now = options.now ?? Date.now;
    this.records.set(localNodeId, {
      nodeId: localNodeId,
      address,
      metadata: structuredClone(metadata),
      version: 1,
      heartbeat: 1,
      lastSeen: this.now(),
      state: 'alive',
    });
  }

  /** 本地节点更新自身元数据（部分字段合并），版本号 +1 */
  updateLocalMetadata(patch: Partial<NodeMetadata>): NodeRecord {
    const record = this.mustGet(this.localNodeId);
    record.metadata = {
      ...record.metadata,
      ...patch,
      labels: { ...record.metadata.labels, ...patch.labels },
      attributes: { ...record.metadata.attributes, ...patch.attributes },
    };
    record.version += 1;
    record.lastSeen = this.now();
    return structuredClone(record);
  }

  /** 本地心跳 +1 */
  heartbeatLocal(): void {
    const record = this.mustGet(this.localNodeId);
    record.heartbeat += 1;
    record.lastSeen = this.now();
    if (record.state !== 'alive') record.state = 'alive';
  }

  /**
   * 合并一条来自 gossip 的远端记录，返回是否接受了更新。
   * 属主节点对自身记录拥有最终权威（SWIM 式自辩），忽略关于自己的远端记录。
   */
  applyRemote(wire: WireRecord): boolean {
    if (wire.nodeId === this.localNodeId) return false;
    const existing = this.records.get(wire.nodeId);
    if (!existing) {
      this.records.set(wire.nodeId, { ...structuredClone(wire), lastSeen: this.now() });
      return true;
    }

    let changed = false;

    // 元数据仲裁：先比版本，同版本比规范化摘要（确定性收敛）
    if (wire.version > existing.version) {
      existing.metadata = structuredClone(wire.metadata);
      existing.version = wire.version;
      changed = true;
    } else if (
      wire.version === existing.version &&
      metadataDigest(wire.metadata) > metadataDigest(existing.metadata)
    ) {
      existing.metadata = structuredClone(wire.metadata);
      changed = true;
    }

    // 心跳取最大，刷新 lastSeen
    if (wire.heartbeat > existing.heartbeat) {
      existing.heartbeat = wire.heartbeat;
      existing.lastSeen = this.now();
      changed = true;
    }

    // 状态传播：更严重的状态获胜（dead > suspect > alive）
    const severity: Record<NodeState, number> = { alive: 0, suspect: 1, dead: 2 };
    if (severity[wire.state] > severity[existing.state]) {
      existing.state = wire.state;
      changed = true;
    }

    if (wire.address !== existing.address) existing.address = wire.address;
    return changed;
  }

  /** 导出本节点视图（用于 gossip 交换） */
  exportWire(): WireRecord[] {
    return [...this.records.values()].map(toWire);
  }

  /** 批量合并远端视图（push-pull 一轮的合并端） */
  mergeRemote(wires: WireRecord[]): number {
    let accepted = 0;
    for (const wire of wires) if (this.applyRemote(wire)) accepted += 1;
    return accepted;
  }

  /** 根据心跳新鲜度刷新各节点状态（suspect/dead 判定） */
  refreshHealth(): void {
    const now = this.now();
    for (const record of this.records.values()) {
      if (record.nodeId === this.localNodeId) continue;
      const silentFor = now - record.lastSeen;
      if (silentFor > this.deadAfterMs) record.state = 'dead';
      else if (silentFor > this.suspectAfterMs && record.state === 'alive') record.state = 'suspect';
    }
  }

  get(nodeId: string): NodeRecord | undefined {
    const record = this.records.get(nodeId);
    return record ? structuredClone(record) : undefined;
  }

  /** 当前健康（alive 且心跳未过期）的节点快照 */
  healthyRecords(): NodeRecord[] {
    this.refreshHealth();
    return [...this.records.values()]
      .filter((record) => record.state === 'alive')
      .map((record) => structuredClone(record));
  }

  allRecords(): NodeRecord[] {
    return [...this.records.values()].map((record) => structuredClone(record));
  }

  private mustGet(nodeId: string): NodeRecord {
    const record = this.records.get(nodeId);
    if (!record) throw new Error('unknown node: ' + nodeId);
    return record;
  }
}
