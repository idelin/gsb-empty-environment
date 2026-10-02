import type { Message } from "./messages.ts";
import { Process } from "./process.ts";
import type { GlobalSnapshot } from "./snapshot.ts";

/** Deterministic PRNG (mulberry32) so runs and tests are reproducible. */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

type SimEvent =
  | { kind: "wakeup"; processId: string }
  | { kind: "deliver"; msg: Message }
  | { kind: "initiate"; processId: string; snapshotId: string };

interface ScheduledEvent {
  time: number;
  seq: number;
  event: SimEvent;
}

export interface SimulationOptions {
  balances: Record<string, number>;
  seed?: number;
  /** max amount per transfer */
  maxTransfer?: number;
  /** [min, max] delivery delay per message */
  channelDelay?: [number, number];
  /** [min, max] gap between a process's transfer decisions */
  thinkTime?: [number, number];
}

/**
 * Discrete-event simulation of a fully-connected cluster of processes
 * communicating over point-to-point FIFO channels with random delays.
 * Business traffic (transfers) never stops for snapshots.
 */
export class Simulation {
  readonly processes: Map<string, Process>;
  readonly initialTotal: number;
  private rand: () => number;
  private queue: ScheduledEvent[] = [];
  private seq = 0;
  private now = 0;
  private nextTransferId = 1;
  private channelFreeAt = new Map<string, number>();
  private maxTransfer: number;
  private channelDelay: [number, number];
  private thinkTime: [number, number];
  /** stats for assertions / demo output */
  transfersSent = 0;
  transfersDelivered = 0;
  transfersDeliveredAfterInitiation = 0;
  private initiatedAt: number | null = null;

  constructor(options: SimulationOptions) {
    this.rand = mulberry32(options.seed ?? 1);
    this.maxTransfer = options.maxTransfer ?? 20;
    this.channelDelay = options.channelDelay ?? [1, 5];
    this.thinkTime = options.thinkTime ?? [1, 3];
    const ids = Object.keys(options.balances);
    this.processes = new Map();
    let total = 0;
    for (const id of ids) {
      const peers = ids.filter((p) => p !== id);
      const proc = new Process(id, options.balances[id], peers);
      proc.send = (msg) => this.enqueueDelivery(msg);
      this.processes.set(id, proc);
      total += options.balances[id];
    }
    this.initialTotal = total;
    for (const id of ids) this.scheduleWakeup(id, 0);
  }

  private intBetween([lo, hi]: [number, number]): number {
    return lo + Math.floor(this.rand() * (hi - lo + 1));
  }

  private push(time: number, event: SimEvent): void {
    this.queue.push({ time, seq: this.seq++, event });
    this.queue.sort((a, b) => a.time - b.time || a.seq - b.seq);
  }

  private scheduleWakeup(processId: string, from: number): void {
    this.push(from + this.intBetween(this.thinkTime), { kind: "wakeup", processId });
  }

  /** FIFO per channel: a message never overtakes an earlier one on the same channel. */
  private enqueueDelivery(msg: Message): void {
    const key = `${msg.from}->${msg.to}`;
    const earliest = Math.max(this.now, this.channelFreeAt.get(key) ?? 0);
    const deliverAt = earliest + this.intBetween(this.channelDelay);
    this.channelFreeAt.set(key, deliverAt);
    this.push(deliverAt, { kind: "deliver", msg });
  }

  /** Schedule a process to initiate a snapshot at a future time. */
  scheduleSnapshotInitiation(processId: string, snapshotId: string, at: number): void {
    this.push(at, { kind: "initiate", processId, snapshotId });
  }

  private onWakeup(processId: string): void {
    const proc = this.processes.get(processId)!;
    // Business as usual: with high probability, fire a transfer to a random peer.
    if (proc.balance >= 1 && this.rand() < 0.8) {
      const peers = [...this.processes.keys()].filter((p) => p !== processId);
      const to = peers[Math.floor(this.rand() * peers.length)];
      const amount = 1 + Math.floor(this.rand() * Math.min(proc.balance, this.maxTransfer));
      proc.sendTransfer(this.nextTransferId++, to, amount, this.now);
      this.transfersSent++;
    }
    this.scheduleWakeup(processId, this.now);
  }

  private onDeliver(msg: Message): void {
    const receiver = this.processes.get(msg.to)!;
    if (msg.kind === "transfer") {
      this.transfersDelivered++;
      if (this.initiatedAt !== null) this.transfersDeliveredAfterInitiation++;
    }
    receiver.receive(msg, this.now);
  }

  /** Run events until `done()` holds or the event budget is exhausted. */
  runUntil(done: () => boolean, maxEvents = 100_000): void {
    while (!done() && this.queue.length > 0 && maxEvents-- > 0) {
      const { time, event } = this.queue.shift()!;
      this.now = time;
      if (event.kind === "wakeup") {
        this.onWakeup(event.processId);
      } else if (event.kind === "deliver") {
        this.onDeliver(event.msg);
      } else {
        this.initiatedAt = this.now;
        this.processes.get(event.processId)!.initiateSnapshot(event.snapshotId, this.now);
      }
    }
    if (!done()) throw new Error("simulation stopped before completion condition was met");
  }

  isSnapshotComplete(snapshotId: string): boolean {
    return [...this.processes.values()].every((p) => p.isSnapshotComplete(snapshotId));
  }

  /** Assemble the consistent global cut recorded by all processes. */
  collectSnapshot(snapshotId: string): GlobalSnapshot {
    return {
      snapshotId,
      processes: [...this.processes.values()].map((p) => p.getSnapshot(snapshotId)),
    };
  }
}
