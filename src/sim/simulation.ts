import { fnv1a, hashState, mix } from "./hash.ts";
import { Prng } from "./prng.ts";
import { Scheduler } from "./scheduler.ts";

export type Handler = (sim: Simulation, payload: unknown) => void;

export interface TraceEntry {
  step: number;
  time: number;
  type: string;
  hash: number;
}

export interface RunStats {
  steps: number;
  time: number;
  finalHash: number;
  exhausted: boolean; // true when the queue drained before maxSteps
}

export interface SimulationOptions {
  seed: number;
  /** Optional user-state hasher folded into every fingerprint. */
  contextHash?: () => number;
  /** Record per-step trace entries (needed for bug reproduction). */
  recordTrace?: boolean;
}

/**
 * Discrete-time, event-driven simulation kernel.
 *
 * Determinism contract: identical seed + identical registered handlers ⇒
 * identical event order, identical fingerprints, identical trace. No wall
 * clock, no Math.random, no platform-dependent behaviour anywhere inside.
 */
export class Simulation {
  readonly prng: Prng;
  readonly seed: number;
  time = 0;
  step = 0;

  private readonly scheduler = new Scheduler();
  private readonly handlers = new Map<string, Handler[]>();
  private readonly contextHash?: () => number;
  private running = false;
  private trace: TraceEntry[] | null;

  constructor(options: SimulationOptions) {
    this.seed = options.seed >>> 0;
    this.prng = new Prng(this.seed);
    this.contextHash = options.contextHash;
    this.trace = options.recordTrace ? [] : null;
  }

  /** Register a handler; returns an unsubscribe function. */
  on(type: string, handler: Handler): () => void {
    const list = this.handlers.get(type) ?? [];
    list.push(handler);
    this.handlers.set(type, list);
    return () => {
      const idx = list.indexOf(handler);
      if (idx >= 0) list.splice(idx, 1);
      if (list.length === 0) this.handlers.delete(type);
    };
  }

  get handlerCount(): number {
    let n = 0;
    for (const list of this.handlers.values()) n += list.length;
    return n;
  }

  get pendingEvents(): number {
    return this.scheduler.size;
  }

  schedule(delay: number, type: string, payload: unknown = null): void {
    this.scheduler.schedule(delay, type, payload, this.time);
  }

  /** Fingerprint of the full simulation state at this instant. */
  fingerprint(): number {
    let h = fnv1a(`t=${this.time};s=${this.step};p=${this.prng.snapshot()};q=${this.scheduler.size}`);
    if (this.contextHash) h = mix(h, this.contextHash());
    return h >>> 0;
  }

  /** Process exactly one event. Returns false when the queue is empty. */
  stepOnce(): boolean {
    const event = this.scheduler.pop();
    if (event === undefined) return false;
    this.time = event.time;
    const list = this.handlers.get(event.type);
    if (list) {
      for (const handler of [...list]) handler(this, event.payload);
    }
    this.step++;
    if (this.trace !== null) {
      this.trace.push({ step: this.step, time: this.time, type: event.type, hash: this.fingerprint() });
    }
    return true;
  }

  /** Run until the queue drains or maxSteps events have been processed. */
  run(maxSteps: number): RunStats {
    if (this.running) {
      throw new Error("re-entrant run(): a simulation is already running");
    }
    this.running = true;
    try {
      let n = 0;
      while (n < maxSteps && this.stepOnce()) n++;
      return {
        steps: n,
        time: this.time,
        finalHash: this.fingerprint(),
        exhausted: this.scheduler.size === 0,
      };
    } finally {
      this.running = false;
    }
  }

  /**
   * Async variant: yields to the event loop every `yieldEvery` steps so
   * multiple simulations can interleave. Each Simulation owns all of its
   * mutable state, so interleaved runs stay bit-for-bit deterministic.
   */
  async runAsync(maxSteps: number, yieldEvery = 4096): Promise<RunStats> {
    if (this.running) {
      throw new Error("re-entrant runAsync(): a simulation is already running");
    }
    this.running = true;
    try {
      let n = 0;
      while (n < maxSteps) {
        if (!this.stepOnce()) break;
        n++;
        if (n % yieldEvery === 0) await Promise.resolve();
      }
      return {
        steps: n,
        time: this.time,
        finalHash: this.fingerprint(),
        exhausted: this.scheduler.size === 0,
      };
    } finally {
      this.running = false;
    }
  }

  getTrace(): readonly TraceEntry[] {
    return this.trace ?? [];
  }

  /** Release all internal state so nothing is retained after a run. */
  dispose(): void {
    this.scheduler.clear();
    this.handlers.clear();
    this.trace = null;
  }
}

export { hashState };
