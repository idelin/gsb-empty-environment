import type { Packet } from './packet.ts';
import { DiffServScheduler } from './scheduler.ts';

export interface CompletedPacket {
  packet: Packet;
  /** Departure time in milliseconds (fractional, simulation clock). */
  departureMs: number;
  /** Queuing + transmission latency in milliseconds. */
  latencyMs: number;
}

/**
 * A bottleneck egress link with fixed capacity, drained through a
 * DiffServScheduler. The simulation clock advances in 1 ms ticks;
 * packets may be transmitted across multiple ticks (store-and-forward
 * with byte-level pacing).
 */
export class BottleneckLink {
  readonly scheduler: DiffServScheduler;
  /** Link capacity in bytes per millisecond (= kbit/s numerically). */
  readonly bytesPerMs: number;
  private nowMs = 0;
  private inService: { packet: Packet; remainingBytes: number } | undefined;
  readonly completed: CompletedPacket[] = [];

  constructor(capacityBytesPerMs: number, scheduler = new DiffServScheduler()) {
    if (!(capacityBytesPerMs > 0)) {
      throw new RangeError('link capacity must be positive');
    }
    this.bytesPerMs = capacityBytesPerMs;
    this.scheduler = scheduler;
  }

  get now(): number {
    return this.nowMs;
  }

  /** Enqueue a packet arriving at the current simulation time. */
  arrive(packet: Packet): void {
    this.scheduler.enqueue(packet);
  }

  /** Advance the clock by one 1 ms tick, transmitting up to capacity. */
  tick(): void {
    let budget = this.bytesPerMs;
    while (budget > 0) {
      if (this.inService === undefined) {
        const next = this.scheduler.dequeue();
        if (next === undefined) break;
        this.inService = { packet: next, remainingBytes: next.sizeBytes };
      }
      const sent = Math.min(budget, this.inService.remainingBytes);
      this.inService.remainingBytes -= sent;
      budget -= sent;
      if (this.inService.remainingBytes === 0) {
        const departureMs = this.nowMs + (1 - budget / this.bytesPerMs);
        this.completed.push({
          packet: this.inService.packet,
          departureMs,
          latencyMs: departureMs - this.inService.packet.arrivalMs,
        });
        this.inService = undefined;
      }
    }
    this.nowMs += 1;
  }

  /**
   * Latency/throughput statistics grouped by flow id.
   * When `windowMs` is given, only packets departed within that offering
   * window are counted and throughput is normalized to the window, so the
   * post-offer drain phase cannot distort the rates.
   */
  statsByFlow(windowMs?: number): Map<string, {
    packets: number;
    bytes: number;
    avgLatencyMs: number;
    maxLatencyMs: number;
    throughputKbps: number;
  }> {
    const completed = windowMs === undefined
      ? this.completed
      : this.completed.filter((c) => c.departureMs <= windowMs);
    const groups = new Map<string, CompletedPacket[]>();
    for (const c of completed) {
      const list = groups.get(c.packet.flowId) ?? [];
      list.push(c);
      groups.set(c.packet.flowId, list);
    }
    const result = new Map<string, {
      packets: number; bytes: number; avgLatencyMs: number;
      maxLatencyMs: number; throughputKbps: number;
    }>();
    for (const [flowId, list] of groups) {
      const bytes = list.reduce((sum, c) => sum + c.packet.sizeBytes, 0);
      const latencies = list.map((c) => c.latencyMs);
      const spanMs = Math.max(windowMs ?? this.nowMs, 1);
      result.set(flowId, {
        packets: list.length,
        bytes,
        avgLatencyMs: latencies.reduce((a, b) => a + b, 0) / latencies.length,
        maxLatencyMs: Math.max(...latencies),
        throughputKbps: (bytes * 8) / spanMs, // bytes/ms == kbit/s
      });
    }
    return result;
  }
}
