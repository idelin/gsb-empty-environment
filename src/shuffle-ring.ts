import type { NodeMeta } from './types.ts';

/**
 * Gossip 洗牌环：把已知成员洗成一个随机环，
 * 每轮 Gossip 依次取环上节点交换样本，走完一圈后重新洗牌。
 */
export class ShuffleRing {
  private readonly selfId: string;
  private ring: NodeMeta[] = [];
  private cursor = 0;

  constructor(selfId: string) {
    this.selfId = selfId;
  }

  /** 用最新成员表重建并洗牌（Fisher–Yates）。 */
  sync(members: Iterable<NodeMeta>): void {
    const peers = [...members].filter((m) => m.id !== this.selfId);
    for (let i = peers.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1));
      [peers[i], peers[j]] = [peers[j], peers[i]];
    }
    this.ring = peers;
    this.cursor = 0;
  }

  /** 取下一个 Gossip 对象；环走完一圈自动重新洗牌。 */
  next(): NodeMeta | undefined {
    if (this.ring.length === 0) return undefined;
    if (this.cursor >= this.ring.length) {
      this.sync(this.ring);
    }
    return this.ring[this.cursor++];
  }

  get size(): number {
    return this.ring.length;
  }

  peers(): NodeMeta[] {
    return [...this.ring];
  }
}
