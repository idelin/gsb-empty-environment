import type { Message, Tick } from "./types.ts";
import { Rng } from "./prng.ts";

export interface Envelope {
  msg: Message;
  from: string;
  to: string;
  deliverTick: Tick;
}

export interface BusOptions {
  lossRate: number;
  dupRate: number;
  minDelay: number;
  maxDelay: number;
  partitions: Array<{ from: Tick; to: Tick }>;
}

/** Deterministic message bus: loss, duplication and delay are all driven by the seeded Rng. */
export class Bus {
  private queue: Envelope[] = [];
  dropped = 0;
  duplicated = 0;
  private rng: Rng;
  private opts: BusOptions;

  constructor(rng: Rng, opts: BusOptions) {
    this.rng = rng;
    this.opts = opts;
  }

  private partitioned(tick: Tick): boolean {
    return this.opts.partitions.some((p) => tick >= p.from && tick <= p.to);
  }

  send(msg: Message, from: string, to: string, tick: Tick): void {
    if (this.partitioned(tick)) {
      this.dropped++;
      return;
    }
    if (this.rng.chance(this.opts.lossRate)) {
      this.dropped++;
      return;
    }
    const delay =
      this.opts.minDelay + this.rng.int(this.opts.maxDelay - this.opts.minDelay + 1);
    this.queue.push({ msg, from, to, deliverTick: tick + delay });
    if (this.rng.chance(this.opts.dupRate)) {
      this.duplicated++;
      this.queue.push({ msg, from, to, deliverTick: tick + delay + 1 });
    }
  }

  deliver(tick: Tick): Envelope[] {
    const due: Envelope[] = [];
    const rest: Envelope[] = [];
    for (const env of this.queue) {
      (env.deliverTick <= tick ? due : rest).push(env);
    }
    this.queue = rest;
    return due;
  }

  get inFlight(): number {
    return this.queue.length;
  }
}
