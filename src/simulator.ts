import { Node } from "./node";
import { Message, NodeId } from "./types";

interface Link {
  a: NodeId;
  b: NodeId;
  cost: number;
  up: boolean;
}

/**
 * 网络模拟器：维护链路状态、按 FIFO 投递消息，
 * 支持分区/重连，并通过周期性摘要交换保证最终一致。
 */
export class Simulator {
  readonly nodes = new Map<NodeId, Node>();
  private readonly links: Link[] = [];
  private queue: Message[] = [];
  readonly stats = {
    delivered: { summary: 0, request: 0, update: 0 },
    enqueuedFill: 0, // request + update 消息数（缺失补齐活动量）
    dropped: 0, // 因链路 down 被丢弃的消息
  };
  /** 消息投递钩子，用于观察补齐行为 */
  onDeliver: ((msg: Message) => void) | null = null;

  addNode(id: NodeId): Node {
    const node = new Node(id);
    this.nodes.set(id, node);
    return node;
  }

  addLink(a: NodeId, b: NodeId, cost = 1): void {
    this.links.push({ a, b, cost, up: true });
    this.node(a).setPeer(b, cost);
    this.node(b).setPeer(a, cost);
  }

  /** 所有节点依据当前链路首次生成自源 LSA */
  refreshAll(): void {
    for (const node of this.nodes.values()) node.refreshSelfLsa();
  }

  setLink(a: NodeId, b: NodeId, up: boolean): void {
    const link = this.link(a, b);
    link.up = up;
    const na = this.node(a);
    const nb = this.node(b);
    if (up) {
      na.setPeer(b, link.cost);
      nb.setPeer(a, link.cost);
      na.refreshSelfLsa();
      nb.refreshSelfLsa();
      // 链路恢复即触发一次摘要交换，启动缺失补齐
      na.sendSummaryTo(b);
      nb.sendSummaryTo(a);
    } else {
      na.removePeer(b);
      nb.removePeer(a);
      na.refreshSelfLsa();
      nb.refreshSelfLsa();
    }
  }

  setLinkCost(a: NodeId, b: NodeId, cost: number): void {
    const link = this.link(a, b);
    link.cost = cost;
    if (link.up) {
      this.node(a).setPeer(b, cost);
      this.node(b).setPeer(a, cost);
      this.node(a).refreshSelfLsa();
      this.node(b).refreshSelfLsa();
    }
  }

  /** 节点撤销（删除）自己的 LSA */
  withdrawNode(id: NodeId): void {
    this.node(id).withdraw();
  }

  /** 节点重新通告（删除后重建 -> 自动使用新代次） */
  restoreNode(id: NodeId): void {
    this.node(id).refreshSelfLsa();
  }

  /** 直接注入一条消息（用于注入过期更新等测试） */
  inject(msg: Message): void {
    this.queue.push(msg);
  }

  isUp(a: NodeId, b: NodeId): boolean {
    const link = this.links.find(
      (l) => (l.a === a && l.b === b) || (l.a === b && l.b === a),
    );
    return !!link && link.up;
  }

  /** 投递队列中所有消息，直到静默 */
  deliverAll(): void {
    this.drainOutboxes();
    while (this.queue.length > 0) {
      const msg = this.queue.shift()!;
      if (!this.isUp(msg.from, msg.to)) {
        this.stats.dropped++;
        continue;
      }
      this.stats.delivered[msg.kind]++;
      this.onDeliver?.(msg);
      const node = this.nodes.get(msg.to);
      if (!node) continue;
      switch (msg.kind) {
        case "summary":
          node.handleSummary(msg.from, msg.vector);
          break;
        case "request":
          node.handleRequest(msg.from, msg.origins);
          break;
        case "update":
          node.handleUpdate(msg.from, msg.lsas);
          break;
      }
      this.drainOutboxes();
    }
  }

  /** 一轮反熵：所有 up 链路两端互发摘要 */
  summaryRound(): void {
    for (const link of this.links) {
      if (!link.up) continue;
      this.node(link.a).sendSummaryTo(link.b);
      this.node(link.b).sendSummaryTo(link.a);
    }
  }

  /**
   * 收敛：先泛洪，再做整轮摘要交换；
   * 若整轮摘要交换没有产生任何补齐（request/update），判定收敛。
   */
  converge(maxRounds = 100): void {
    for (let round = 0; round < maxRounds; round++) {
      this.deliverAll();
      const before = this.stats.enqueuedFill;
      this.summaryRound();
      this.deliverAll();
      if (this.stats.enqueuedFill === before) return;
    }
    throw new Error("simulation did not converge");
  }

  private drainOutboxes(): void {
    for (const node of this.nodes.values()) {
      for (const msg of node.drainOutbox()) {
        if (msg.kind !== "summary") this.stats.enqueuedFill++;
        this.queue.push(msg);
      }
    }
  }

  private node(id: NodeId): Node {
    const node = this.nodes.get(id);
    if (!node) throw new Error(`unknown node ${id}`);
    return node;
  }

  private link(a: NodeId, b: NodeId): Link {
    const link = this.links.find(
      (l) => (l.a === a && l.b === b) || (l.a === b && l.b === a),
    );
    if (!link) throw new Error(`no link ${a}-${b}`);
    return link;
  }
}
