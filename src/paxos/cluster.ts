import { Network } from './network.ts';
import { PaxosNode } from './node.ts';

/**
 * 确定性集群仿真器：逻辑时钟 + 消息泵。
 * pump() 反复投递直到全网静默（无在途消息），保证测试可重现。
 */
export class Cluster {
  readonly network = new Network();
  readonly nodes = new Map<string, PaxosNode>();
  readonly leaseTtl: number;
  #now = 0;

  constructor(size: number, leaseTtl = 1000) {
    this.leaseTtl = leaseTtl;
    const ids = Array.from({ length: size }, (_, i) => `n${i + 1}`);
    for (const id of ids) this.network.register(id);
    for (const id of ids) {
      this.nodes.set(
        id,
        new PaxosNode({
          id,
          peers: ids,
          network: this.network,
          clock: () => this.#now,
          leaseTtl,
        }),
      );
    }
  }

  get now(): number {
    return this.#now;
  }

  advanceTime(ms: number): void {
    this.#now += ms;
  }

  node(id: string): PaxosNode {
    const node = this.nodes.get(id);
    if (!node) throw new Error(`unknown node: ${id}`);
    return node;
  }

  /** 投递所有在途消息直到全网静默；返回投递的消息总数。 */
  pump(): number {
    let delivered = 0;
    for (;;) {
      let round = 0;
      for (const [id, node] of this.nodes) {
        let msg;
        while ((msg = this.network.dequeue(id)) !== undefined) {
          node.handle(msg);
          round += 1;
        }
      }
      delivered += round;
      if (round === 0) return delivered;
    }
  }

  partition(groupA: string[], groupB: string[]): void {
    this.network.partition(groupA, groupB);
  }

  heal(): void {
    this.network.heal();
  }

  /** 全网在途消息数（泄漏断言用）。 */
  pendingMessages(): number {
    return this.network.pendingCount();
  }

  /** 所有节点的已提交前缀（applied）是否一致。 */
  committedLogs(): unknown[][] {
    return [...this.nodes.values()].map((n) => [...n.applied]);
  }
}
