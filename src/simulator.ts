import { Network } from './network.ts';
import { LeaseNode } from './node.ts';
import type { LeaseConfig, MessageType, SimEvent } from './types.ts';

/**
 * 单机离散 tick 模拟器:
 * 每个 tick 依次执行 场景动作 -> 消息投递(0 延迟消息同 tick 级联)-> 节点定时器,
 * 全部事件带 tick 记录到事件日志。
 */
export class Simulator {
  tick = 0;
  readonly events: SimEvent[] = [];
  readonly network: Network;
  readonly nodes = new Map<string, LeaseNode>();
  readonly majority: number;
  private readonly actions = new Map<number, Array<() => void>>();
  private readonly tickListeners: Array<(sim: Simulator) => void> = [];

  readonly config: LeaseConfig;

  constructor(config: LeaseConfig) {
    this.config = config;
    this.majority = Math.floor(config.nodeIds.length / 2) + 1;
    this.network = new Network(
      (type) => (type === 'LeaseRequest' || type === 'Heartbeat' ? config.networkDelay : 0),
      (msg) =>
        this.log(null, 'MSG_DROPPED', `id=${msg.id} ${msg.type} ${msg.from}->${msg.to} (blackhole)`),
      () => this.tick,
    );
    for (const id of config.nodeIds) {
      this.nodes.set(id, new LeaseNode(id, this));
    }
  }

  log(node: string | null, type: string, detail: string): void {
    this.events.push({ tick: this.tick, node, type, detail });
  }

  send(type: MessageType, from: string, to: string, term: number, reason?: string): void {
    const msg = this.network.send(type, from, to, term, reason);
    if (msg) {
      this.log(from, 'MSG_SENT', `id=${msg.id} ${type} ${from}->${to} term=${term} deliverAt=${msg.deliverTick}`);
    }
  }

  /** 在指定 tick 的开头安排一个场景动作(如发起选举、注入黑洞)。 */
  at(tick: number, action: () => void): void {
    const list = this.actions.get(tick) ?? [];
    list.push(action);
    this.actions.set(tick, list);
  }

  /** 每个 tick 末尾调用(可用于校验不变式,如"至多一个可写主")。 */
  addTickListener(listener: (sim: Simulator) => void): void {
    this.tickListeners.push(listener);
  }

  startElection(nodeId: string): void {
    this.node(nodeId).campaign();
  }

  clientWrite(nodeId: string, value: string): boolean {
    return this.node(nodeId).clientWrite(value);
  }

  node(nodeId: string): LeaseNode {
    const n = this.nodes.get(nodeId);
    if (!n) throw new Error(`unknown node ${nodeId}`);
    return n;
  }

  step(): void {
    this.tick += 1;
    for (const action of this.actions.get(this.tick) ?? []) action();
    let due = this.network.takeDue(this.tick);
    while (due.length > 0) {
      for (const msg of due) {
        this.log(msg.to, 'MSG_DELIVERED', `id=${msg.id} ${msg.type} ${msg.from}->${msg.to} term=${msg.term}`);
        this.node(msg.to).onMessage(msg);
      }
      due = this.network.takeDue(this.tick);
    }
    for (const node of this.nodes.values()) node.onTick();
    for (const listener of this.tickListeners) listener(this);
  }

  runUntil(tick: number): void {
    while (this.tick < tick) this.step();
  }

  eventsOfType(type: string): SimEvent[] {
    return this.events.filter((e) => e.type === type);
  }
}
