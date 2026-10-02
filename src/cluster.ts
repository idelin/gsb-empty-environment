import { Node, type NodeOptions } from "./node.ts";

export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export class Cluster {
  readonly nodes = new Map<string, Node>();
  private readonly active = new Set<string>();
  period = 0;
  private readonly random: () => number;
  private readonly nodeOptions: NodeOptions;

  constructor(random: () => number = Math.random, nodeOptions: NodeOptions = {}) {
    this.random = random;
    this.nodeOptions = nodeOptions;
  }

  get size(): number {
    return this.nodes.size;
  }

  createNode(id: string): Node {
    const node = new Node(id, this.nodeOptions);
    this.nodes.set(id, node);
    this.active.add(id);
    for (const n of this.nodes.values()) n.setClusterSize(this.nodes.size);
    return node;
  }

  join(nodeId: string, introducerId: string): void {
    const node = this.nodes.get(nodeId);
    const introducer = this.nodes.get(introducerId);
    if (!node || !introducer) throw new Error("unknown node or introducer");
    const reply = introducer.handlePacket(node.makeJoin(introducerId));
    if (reply) node.handlePacket(reply);
    node.announceJoin();
  }

  leave(nodeId: string): void {
    const node = this.nodes.get(nodeId);
    if (!node) return;
    const event = node.leave();
    const peer = this.pickPeer(nodeId);
    if (peer) {
      this.nodes.get(peer)!.handlePacket({
        kind: "ack",
        from: nodeId,
        to: peer,
        seq: 0,
        piggyback: [event],
      });
    }
    this.active.delete(nodeId);
  }

  runPeriod(): void {
    this.period += 1;
    const order = [...this.active].sort(() => this.random() - 0.5);
    for (const id of order) {
      if (!this.active.has(id)) continue;
      const node = this.nodes.get(id)!;
      const target = this.pickPeer(id);
      if (!target) continue;
      const ack = this.nodes
        .get(target)!
        .handlePacket(node.makePing(target));
      if (ack) node.handlePacket(ack);
    }
  }

  informedCount(nodeId: string): number {
    let count = 0;
    for (const node of this.nodes.values()) {
      if (node.members.statusOf(nodeId) === "alive") count += 1;
    }
    return count;
  }

  pendingGossipTotal(): number {
    let total = 0;
    for (const node of this.nodes.values()) total += node.gossip.pendingCount;
    return total;
  }

  private pickPeer(selfId: string): string | null {
    const candidates = [...this.active].filter((id) => id !== selfId);
    if (candidates.length === 0) return null;
    return candidates[Math.floor(this.random() * candidates.length)];
  }
}
