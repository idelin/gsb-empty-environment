import { DiffServScheduler } from './scheduler.ts';
import type { Packet, QueueId } from './packet.ts';

export interface TransmissionRecord {
  packet: Packet;
  queue: QueueId;
  /** Time (s) the packet started transmission on the link. */
  startTime: number;
  /** Time (s) the last bit left the link. */
  finishTime: number;
  /** startTime - arrivalTime: how long the packet waited in its queue. */
  queueingDelay: number;
}

export interface SimResult {
  records: TransmissionRecord[];
  /** Total simulated busy time until the last packet left. */
  duration: number;
}

/**
 * Discrete-event simulation of a single bandwidth-limited egress link
 * fed by a DiffServ scheduler. Transmission is non-preemptive: once a
 * packet starts, it occupies the link for sizeBytes / rate seconds.
 */
export function simulateLink(
  packets: Packet[],
  rateBytesPerSec: number,
  scheduler: DiffServScheduler = new DiffServScheduler(),
): SimResult {
  if (!(rateBytesPerSec > 0)) {
    throw new RangeError('Link rate must be positive');
  }
  const arrivals = [...packets].sort((a, b) => a.arrivalTime - b.arrivalTime);
  const records: TransmissionRecord[] = [];
  let linkFreeAt = 0;
  let next = 0;

  while (next < arrivals.length || scheduler.size > 0) {
    const nextArrivalAt = next < arrivals.length ? arrivals[next].arrivalTime : Infinity;
    if (scheduler.size > 0 && linkFreeAt <= nextArrivalAt) {
      const { packet, queue } = scheduler.dequeue()!;
      const startTime = linkFreeAt;
      const finishTime = startTime + packet.sizeBytes / rateBytesPerSec;
      records.push({
        packet,
        queue,
        startTime,
        finishTime,
        queueingDelay: startTime - packet.arrivalTime,
      });
      linkFreeAt = finishTime;
    } else {
      const arrival = arrivals[next++];
      scheduler.enqueue(arrival);
      if (linkFreeAt < arrival.arrivalTime) linkFreeAt = arrival.arrivalTime;
    }
  }

  return { records, duration: linkFreeAt };
}

export interface ClassStats {
  queue: QueueId;
  packets: number;
  bytes: number;
  avgDelayMs: number;
  maxDelayMs: number;
  /** Delivered goodput in bytes/sec over the simulation window. */
  throughputBps: number;
}

export function statsByQueue(result: SimResult, windowSec?: number): ClassStats[] {
  const window = windowSec ?? result.duration;
  const byQueue = new Map<QueueId, TransmissionRecord[]>();
  for (const rec of result.records) {
    // Only count packets that started transmission inside the measurement
    // window; backlog draining after the window is not delivered goodput.
    if (rec.startTime >= window) continue;
    const list = byQueue.get(rec.queue) ?? [];
    list.push(rec);
    byQueue.set(rec.queue, list);
  }
  const stats: ClassStats[] = [];
  for (const [queue, recs] of byQueue) {
    const bytes = recs.reduce((sum, r) => sum + r.packet.sizeBytes, 0);
    const delays = recs.map((r) => r.queueingDelay * 1000);
    stats.push({
      queue,
      packets: recs.length,
      bytes,
      avgDelayMs: delays.reduce((a, b) => a + b, 0) / delays.length,
      maxDelayMs: Math.max(...delays),
      throughputBps: bytes / window,
    });
  }
  const order: QueueId[] = ['EF', 'AF4', 'AF3', 'AF2', 'AF1', 'BE'];
  return stats.sort((a, b) => order.indexOf(a.queue) - order.indexOf(b.queue));
}
