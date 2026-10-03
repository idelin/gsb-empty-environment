import { Simulator } from './simulator.ts';

export type MessageHandler = (msg: unknown) => void;

/**
 * Simulated network with per-node packet counters (in/out) and
 * uniformly distributed delivery latency.
 */
export class Network {
  readonly sent: Uint32Array;
  readonly received: Uint32Array;
  private handlers: Array<MessageHandler | null>;
  private sim: Simulator;
  private rng: () => number;
  private minLatencyMs: number;
  private maxLatencyMs: number;

  constructor(
    sim: Simulator,
    nodeCount: number,
    rng: () => number,
    minLatencyMs = 1,
    maxLatencyMs = 5,
  ) {
    this.sim = sim;
    this.rng = rng;
    this.minLatencyMs = minLatencyMs;
    this.maxLatencyMs = maxLatencyMs;
    this.sent = new Uint32Array(nodeCount);
    this.received = new Uint32Array(nodeCount);
    this.handlers = new Array<MessageHandler | null>(nodeCount).fill(null);
  }

  register(id: number, handler: MessageHandler): void {
    this.handlers[id] = handler;
  }

  unregister(id: number): void {
    this.handlers[id] = null;
  }

  send(from: number, to: number, msg: unknown): void {
    if (from === to) return;
    this.sent[from]++;
    const latency =
      this.minLatencyMs + this.rng() * (this.maxLatencyMs - this.minLatencyMs);
    this.sim.schedule(latency, () => {
      const handler = this.handlers[to];
      if (handler == null) return; // receiver crashed: packet dropped
      this.received[to]++;
      handler(msg);
    });
  }

  totalPackets(): number {
    let total = 0;
    for (let i = 0; i < this.sent.length; i++) total += this.sent[i] + this.received[i];
    return total;
  }
}
