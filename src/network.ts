import { SimNode } from './node.ts';
import type { Message } from './node.ts';

interface Edge {
  a: string;
  b: string;
  up: boolean;
}

interface Event {
  time: number;
  seq: number;
  from: string;
  to: string;
  msg: Message;
}

function edgeKey(a: string, b: string): string {
  return a < b ? `${a}|${b}` : `${b}|${a}`;
}

export function mulberry32(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s |= 0;
    s = (s + 0x6d2b79f5) | 0;
    let t = Math.imul(s ^ (s >>> 15), 1 | s);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export class Network {
  readonly nodes = new Map<string, SimNode>();
  private readonly edges = new Map<string, Edge>();
  private queue: Event[] = [];
  private readonly rand: () => number;
  private seq = 0;
  now = 0;
  delivered = 0;
  dropped = 0;

  constructor(seed: number) {
    this.rand = mulberry32(seed);
  }

  random(): number {
    return this.rand();
  }

  addNode(id: string): SimNode {
    const node = new SimNode(id, this);
    this.nodes.set(id, node);
    return node;
  }

  addEdge(a: string, b: string): void {
    this.edges.set(edgeKey(a, b), { a, b, up: true });
  }

  setEdgeUp(a: string, b: string, up: boolean): void {
    const edge = this.edges.get(edgeKey(a, b));
    if (!edge) throw new Error(`no edge ${a}-${b}`);
    const wasUp = edge.up;
    edge.up = up;
    if (up && !wasUp) {
      this.node(a).sendSummaryTo(b);
      this.node(b).sendSummaryTo(a);
    }
  }

  node(id: string): SimNode {
    const n = this.nodes.get(id);
    if (!n) throw new Error(`unknown node ${id}`);
    return n;
  }

  upNeighbors(id: string): string[] {
    const out: string[] = [];
    for (const e of this.edges.values()) {
      if (!e.up) continue;
      if (e.a === id) out.push(e.b);
      else if (e.b === id) out.push(e.a);
    }
    return out.sort();
  }

  send(from: string, to: string, msg: Message, latency?: number): void {
    const edge = this.edges.get(edgeKey(from, to));
    if (!edge || !edge.up) {
      this.dropped++;
      return;
    }
    const delay = latency ?? 1 + Math.floor(this.rand() * 5);
    this.queue.push({ time: this.now + delay, seq: this.seq++, from, to, msg });
    this.queue.sort((x, y) => x.time - y.time || x.seq - y.seq);
  }

  run(maxEvents = 100000): void {
    let processed = 0;
    while (this.queue.length > 0) {
      if (++processed > maxEvents) throw new Error('event loop did not quiesce');
      const ev = this.queue.shift()!;
      this.now = ev.time;
      this.delivered++;
      this.node(ev.to).receive(ev.msg, ev.from);
    }
  }
}
