import { Endpoint } from './endpoint.ts';
import { genKey, type NodeId, type Packet } from './types.ts';

export interface NetworkHooks {
  drop?: (pkt: Packet) => boolean;
  extraDelay?: (pkt: Packet) => number;
  duplicates?: (pkt: Packet) => number;
}

interface InFlight {
  at: number;
  pkt: Packet;
  order: number;
}

export class Simulator {
  now = 0;
  latency: number;
  hooks: NetworkHooks;
  verbose = false;
  readonly sentLog: Packet[] = [];
  readonly trace: string[] = [];
  private queue: InFlight[] = [];
  private order = 0;
  private readonly endpoints = new Map<NodeId, Endpoint>();

  constructor(opts: { latency?: number; hooks?: NetworkHooks } = {}) {
    this.latency = opts.latency ?? 10;
    this.hooks = opts.hooks ?? {};
  }

  add(ep: Endpoint): void {
    this.endpoints.set(ep.id, ep);
  }

  emit(pkt: Packet): void {
    this.sentLog.push(pkt);
    if (this.hooks.drop?.(pkt) === true) {
      this.record(`t=${this.now} DROP ${this.fmt(pkt)}`);
      return;
    }
    const copies = 1 + (this.hooks.duplicates?.(pkt) ?? 0);
    for (let i = 0; i < copies; i += 1) {
      const at = this.now + this.latency + (this.hooks.extraDelay?.(pkt) ?? 0);
      this.queue.push({ at, pkt, order: this.order });
      this.order += 1;
    }
  }

  inject(pkt: Packet, at: number): void {
    this.queue.push({ at, pkt, order: this.order });
    this.order += 1;
  }

  step(): boolean {
    let nextPktAt = Infinity;
    for (const f of this.queue) nextPktAt = Math.min(nextPktAt, f.at);
    let nextTimerAt = Infinity;
    let timerEp: Endpoint | undefined;
    for (const ep of this.endpoints.values()) {
      const t = ep.nextTimerAt();
      if (t !== undefined && t < nextTimerAt) {
        nextTimerAt = t;
        timerEp = ep;
      }
    }
    if (nextPktAt === Infinity && nextTimerAt === Infinity) return false;
    if (nextTimerAt <= nextPktAt) {
      this.now = Math.max(this.now, nextTimerAt);
      const out = (timerEp as Endpoint).tick(this.now);
      for (const p of out) this.emit(p);
      return true;
    }
    this.now = Math.max(this.now, nextPktAt);
    const due = this.queue.filter((f) => f.at <= this.now).sort((a, b) => a.order - b.order);
    this.queue = this.queue.filter((f) => f.at > this.now);
    for (const f of due) this.deliver(f.pkt);
    return true;
  }

  settle(maxSteps = 10_000): void {
    let n = 0;
    while (this.step()) {
      n += 1;
      if (n > maxSteps) throw new Error('simulation did not settle');
    }
  }

  runUntil(cond: () => boolean, maxTime = 100_000): void {
    while (!cond()) {
      if (this.now > maxTime || !this.step()) {
        throw new Error(`condition not met by t=${this.now}`);
      }
    }
  }

  private deliver(pkt: Packet): void {
    const ep = this.endpoints.get(pkt.dst);
    if (ep === undefined) return;
    this.record(`t=${this.now} ${this.fmt(pkt)}`);
    const out = ep.onPacket(pkt, this.now);
    for (const p of out) this.emit(p);
  }

  private fmt(pkt: Packet): string {
    const sg = pkt.srcGen === null ? '-' : genKey(pkt.srcGen);
    const dg = pkt.dstGen === null ? '-' : genKey(pkt.dstGen);
    const payload = pkt.payload.length > 0 ? ` "${pkt.payload}"` : '';
    return `${pkt.src}->${pkt.dst} ${pkt.kind} ${sg}->${dg} seq=${pkt.seq} ack=${pkt.ack}${payload}`;
  }

  private record(line: string): void {
    this.trace.push(line);
    if (this.verbose) console.log(line);
  }
}
