import { MemberList, type MemberListOptions } from './member-list.ts';
import type { NodeMetadata } from './types.ts';

export interface GossipClusterOptions extends MemberListOptions {
  /** 注入随机源，便于测试确定性 */
  random?: () => number;
}

/**
 * 进程内模拟的 Gossip 集群：每轮每个成员随机选取一个对等节点做
 * push-pull 全状态交换（反熵），若干轮后全网视图收敛。
 */
export class GossipCluster {
  private readonly members = new Map<string, MemberList>();
  private readonly random: () => number;
  private readonly options: GossipClusterOptions;

  constructor(options: GossipClusterOptions = {}) {
    this.options = options;
    this.random = options.random ?? Math.random;
  }

  addNode(nodeId: string, address: string, metadata: NodeMetadata): MemberList {
    if (this.members.has(nodeId)) throw new Error('duplicate node: ' + nodeId);
    const member = new MemberList(nodeId, address, metadata, this.options);
    // 让新节点立刻感知已存在的成员（模拟 join 时的全量推送）
    for (const other of this.members.values()) {
      member.mergeRemote(other.exportWire());
      other.mergeRemote(member.exportWire());
    }
    this.members.set(nodeId, member);
    return member;
  }

  get(nodeId: string): MemberList {
    const member = this.members.get(nodeId);
    if (!member) throw new Error('unknown member: ' + nodeId);
    return member;
  }

  nodeIds(): string[] {
    return [...this.members.keys()];
  }

  /** 执行一轮 gossip：每个成员随机挑一个对等节点做 push-pull 交换 */
  gossipRound(): void {
    const ids = this.nodeIds();
    if (ids.length < 2) return;
    for (const id of ids) {
      const peers = ids.filter((peer) => peer !== id);
      const peerId = peers[Math.floor(this.random() * peers.length)];
      const a = this.members.get(id)!;
      const b = this.members.get(peerId)!;
      const aView = a.exportWire();
      const bView = b.exportWire();
      a.mergeRemote(bView);
      b.mergeRemote(aView);
    }
  }

  /** 循环 gossip 直到全网收敛（一轮内无任何记录被接受即视为收敛） */
  converge(maxRounds = 50): number {
    for (let round = 1; round <= maxRounds; round += 1) {
      const before = this.fingerprint();
      this.gossipRound();
      if (this.fingerprint() === before) return round;
    }
    return maxRounds;
  }

  /** 全网视图指纹：所有成员视图拼接后的摘要，用于检测收敛 */
  fingerprint(): string {
    return this.nodeIds()
      .sort()
      .map((id) => {
        const member = this.members.get(id)!;
        return member
          .exportWire()
          .map((wire) => `${wire.nodeId}@${wire.version}/${wire.heartbeat}/${wire.state}:${wire.metadata.weight}`)
          .join('|');
      })
      .join('##');
  }
}
