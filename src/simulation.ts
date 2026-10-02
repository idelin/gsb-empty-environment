import { LeaseNode } from './node.ts';
import { DEFAULT_CONFIG, majorityOf, type SimConfig } from './config.ts';
import type { Message, MessageKind, SimEvent } from './types.ts';

const REPLY_KINDS: ReadonlySet<MessageKind> = new Set([
  'GrantLease',
  'RejectLease',
  'HeartbeatAck',
]);

interface Blackhole {
  start: number;
  end: number;
  pairs: [string, string][];
}

/**
 * 单机离散 tick 模拟器：
 * - 每个 tick 依次：投递到期消息 -> 执行调度动作 -> 运行节点定时器 -> 快照写能力。
 * - 网络模型：单向延迟（请求 forwardLatency / 应答 ackLatency），可注入黑洞按投递时刻丢包。
 */
export class Simulation {
  tick = 0;
  readonly nodes = new Map<string, LeaseNode>();
  readonly events: SimEvent[] = [];
  /** 每个 tick 具备写能力的主节点列表，用于双主不变量校验 */
  readonly writeCapableTimeline: { tick: number; primaries: string[] }[] = [];

  private queue: Message[] = [];
  private blackholes: Blackhole[] = [];
  private scheduled = new Map<number, ((sim: Simulation) => void)[]>();

  readonly config: SimConfig;

  constructor(config: SimConfig = DEFAULT_CONFIG) {
    this.config = config;
  }

  get majority(): number {
    return majorityOf(this.config);
  }

  addNode(id: string): LeaseNode {
    const node = new LeaseNode(id, this);
    this.nodes.set(id, node);
    return node;
  }

  log(node: string, event: string, detail: string): void {
    this.events.push({ tick: this.tick, node, event, detail });
  }

  send(from: string, to: string, kind: MessageKind, fields: Partial<Message> = {}): void {
    const latency = REPLY_KINDS.has(kind)
      ? this.config.ackLatency
      : this.config.forwardLatency;
    this.queue.push({
      from,
      to,
      kind,
      epoch: 0,
      ...fields,
      deliverAt: this.tick + latency,
    } as Message);
  }

  /** 注入网络黑洞：[start, end) 内投递到 pairs 链路的消息全部丢弃 */
  injectBlackhole(start: number, end: number, pairs: [string, string][]): void {
    this.blackholes.push({ start, end, pairs });
    this.log(
      '-',
      'blackhole-injected',
      `links ${pairs.map((p) => p.join('<->')).join(', ')} down for ticks [${start}, ${end})`,
    );
  }

  private isBlackholed(from: string, to: string, tick: number): boolean {
    return this.blackholes.some(
      (b) =>
        tick >= b.start &&
        tick < b.end &&
        b.pairs.some(
          ([x, y]) => (x === from && y === to) || (x === to && y === from),
        ),
    );
  }

  schedule(tick: number, fn: (sim: Simulation) => void): void {
    const list = this.scheduled.get(tick) ?? [];
    list.push(fn);
    this.scheduled.set(tick, list);
  }

  /** 客户端写请求：仅漂移栅栏内的主节点可写 */
  write(nodeId: string, data: string): boolean {
    const node = this.nodes.get(nodeId);
    if (!node) throw new Error(`unknown node ${nodeId}`);
    if (node.isWriteCapable()) {
      this.log(
        nodeId,
        'write-accepted',
        `${data} (lease until ${node.leaseExpiry}, drift fence ${node.leaseExpiry - this.config.maxClockDrift})`,
      );
      return true;
    }
    const reason =
      node.role !== 'primary'
        ? `not primary (role=${node.role})`
        : `drift fence (local ${node.localNow} >= ${node.leaseExpiry - this.config.maxClockDrift})`;
    this.log(nodeId, 'write-rejected', `${data}: ${reason}`);
    return false;
  }

  step(): void {
    const due = this.queue.filter((m) => m.deliverAt === this.tick);
    this.queue = this.queue.filter((m) => m.deliverAt !== this.tick);
    for (const msg of due) {
      if (this.isBlackholed(msg.from, msg.to, this.tick)) {
        this.log(
          msg.to,
          'message-dropped',
          `${msg.kind} ${msg.from}->${msg.to} (network blackhole)`,
        );
        continue;
      }
      this.nodes.get(msg.to)!.onMessage(msg);
    }
    for (const fn of this.scheduled.get(this.tick) ?? []) fn(this);
    for (const node of this.nodes.values()) node.onTick();
    const primaries = [...this.nodes.values()]
      .filter((n) => n.isWriteCapable())
      .map((n) => n.id);
    this.writeCapableTimeline.push({ tick: this.tick, primaries });
  }

  run(untilTick: number): void {
    for (; this.tick <= untilTick; this.tick++) this.step();
  }

  find(event: string, pred?: (e: SimEvent) => boolean): SimEvent | undefined {
    return this.events.find((e) => e.event === event && (!pred || pred(e)));
  }

  filter(event: string, pred?: (e: SimEvent) => boolean): SimEvent[] {
    return this.events.filter((e) => e.event === event && (!pred || pred(e)));
  }
}
