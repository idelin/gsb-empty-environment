import { SimulatedNetwork } from './network.ts';
import { PeerNode } from './node.ts';

export interface SimEvent {
  tick: number;
  nodeId: number;
  event: string;
  detail: string;
}

/**
 * 集群模拟驱动器：推进离散 tick、注入分区/愈合、
 * 判定收敛并充当死锁看门狗（有界 tick 内必须收敛）。
 */
export class Cluster {
  readonly nodes: PeerNode[] = [];
  readonly network = new SimulatedNetwork();
  readonly events: SimEvent[] = [];
  tick = 0;

  readonly size: number;

  constructor(size = 10) {
    this.size = size;
    const ids = Array.from({ length: size }, (_, i) => i + 1);
    for (const id of ids) {
      this.nodes.push(
        new PeerNode(id, ids, this.network, (nodeId, event, detail, tick) => {
          this.events.push({ tick, nodeId, event, detail });
        }),
      );
    }
  }

  node(id: number): PeerNode {
    const node = this.nodes.find((n) => n.id === id);
    if (!node) throw new Error(`unknown node ${id}`);
    return node;
  }

  step(): void {
    this.network.deliver(this.tick);
    for (const node of this.nodes) node.onTick(this.tick);
    this.tick++;
  }

  run(ticks: number): void {
    for (let i = 0; i < ticks; i++) this.step();
  }

  partition(groupA: number[], groupB: number[]): void {
    this.network.partition(groupA, groupB);
    this.events.push({ tick: this.tick, nodeId: 0, event: 'partition', detail: `[${groupA}]|[${groupB}]` });
  }

  heal(): void {
    this.network.heal();
    this.events.push({ tick: this.tick, nodeId: 0, event: 'heal', detail: 'all links restored' });
  }

  /** 收敛 = 全部节点握手结束、域一致、视图一致且全员存活。 */
  converged(): boolean {
    const reference = this.nodes[0];
    return this.nodes.every(
      (n) =>
        n.mergeState === 'idle' &&
        n.partitionInfo().partitionId === reference.partitionInfo().partitionId &&
        n.aliveCount() === this.size &&
        n.viewString() === reference.viewString(),
    );
  }

  /**
   * 运行直至收敛；超过 maxTicks 未收敛即判定死锁并抛出诊断信息。
   * 返回收敛所用 tick 数。
   */
  runUntilConverged(maxTicks: number): number {
    for (let i = 0; i < maxTicks; i++) {
      this.step();
      if (this.converged()) return i + 1;
    }
    const diagnostics = this.nodes
      .map((n) => `  node=${n.id} domain=${n.partitionInfo().partitionId} alive=${n.aliveCount()} merge=${n.mergeState}`)
      .join('\n');
    throw new Error(`deadlock suspected: not converged within ${maxTicks} ticks\n${diagnostics}`);
  }

  clocks(): number[] {
    return this.nodes.map((n) => n.clockRead());
  }

  partitionIds(): string[] {
    return this.nodes.map((n) => n.partitionInfo().partitionId);
  }

  viewStrings(): string[] {
    return this.nodes.map((n) => n.viewString());
  }

  eventsNamed(event: string): SimEvent[] {
    return this.events.filter((e) => e.event === event);
  }
}
