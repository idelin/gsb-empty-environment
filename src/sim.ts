/**
 * Discrete-time simulation harness: message queue + partition control.
 * No sockets, no timers — tests drive ticks and delivery explicitly.
 */

import { RaftNode } from "./raft.ts";
import type { Message, NodeId } from "./raft.ts";

export interface NodeSpec {
  id: NodeId;
  electionTimeout: number;
  heartbeatInterval: number;
}

interface Envelope {
  from: NodeId;
  to: NodeId;
  msg: Message;
}

export class Cluster {
  readonly nodes = new Map<NodeId, RaftNode>();
  private queue: Envelope[] = [];
  private blockedLinks = new Set<string>(); // "from>to" directed
  time = 0;

  constructor(specs: NodeSpec[]) {
    const ids = specs.map((s) => s.id);
    for (const spec of specs) {
      const node = new RaftNode({
        id: spec.id,
        peers: ids.filter((id) => id !== spec.id),
        electionTimeout: spec.electionTimeout,
        heartbeatInterval: spec.heartbeatInterval,
        send: (to, msg) => this.enqueue(spec.id, to, msg),
      });
      this.nodes.set(spec.id, node);
    }
  }

  node(id: NodeId): RaftNode {
    const node = this.nodes.get(id);
    if (!node) throw new Error(`unknown node ${id}`);
    return node;
  }

  /** Advance every node by n ticks, delivering messages after each tick. */
  tick(n = 1): void {
    for (let i = 0; i < n; i++) {
      this.time += 1;
      for (const node of this.nodes.values()) node.tick();
      this.flush();
    }
  }

  /** Deliver queued messages until quiescent; messages on blocked links drop. */
  flush(): void {
    while (this.queue.length > 0) {
      const batch = this.queue;
      this.queue = [];
      for (const env of batch) {
        if (this.blockedLinks.has(`${env.from}>${env.to}`)) continue;
        this.node(env.to).receive(env.msg);
      }
    }
  }

  block(from: NodeId, to: NodeId): void {
    this.blockedLinks.add(`${from}>${to}`);
  }

  unblock(from: NodeId, to: NodeId): void {
    this.blockedLinks.delete(`${from}>${to}`);
  }

  /** Cut a node off from the rest of the cluster (both directions). */
  isolate(id: NodeId): void {
    for (const other of this.nodes.keys()) {
      if (other === id) continue;
      this.block(id, other);
      this.block(other, id);
    }
  }

  healAll(): void {
    this.blockedLinks.clear();
  }

  private enqueue(from: NodeId, to: NodeId, msg: Message): void {
    this.queue.push({ from, to, msg });
  }
}
