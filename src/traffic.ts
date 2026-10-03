import { DSCP } from './packet.ts';
import type { Packet } from './packet.ts';

let nextId = 1;
export function resetPacketIds(): void {
  nextId = 1;
}

export interface FlowOptions {
  flowId: string;
  dscp: number;
  /** First packet arrival time (s). */
  start: number;
  /** Flow duration (s). */
  duration: number;
  /** Inter-arrival gap (s). */
  interval: number;
  sizeBytes: number;
}

/** Constant-bit-rate packet flow. */
export function cbrFlow(opts: FlowOptions): Packet[] {
  const packets: Packet[] = [];
  for (let t = opts.start; t < opts.start + opts.duration; t += opts.interval) {
    packets.push({
      id: nextId++,
      dscp: opts.dscp,
      sizeBytes: opts.sizeBytes,
      arrivalTime: t,
      flowId: opts.flowId,
    });
  }
  return packets;
}

/** G.711-style voice flow: 160-byte frames every 20 ms, marked EF. */
export function voiceFlow(flowId: string, start: number, duration: number): Packet[] {
  return cbrFlow({ flowId, dscp: DSCP.EF, start, duration, interval: 0.02, sizeBytes: 160 });
}

/** Bulk download flow: MTU-sized BE packets at the given offered rate. */
export function bulkFlow(
  flowId: string,
  start: number,
  duration: number,
  offeredBytesPerSec: number,
  sizeBytes = 1500,
): Packet[] {
  return cbrFlow({
    flowId,
    dscp: DSCP.CS0,
    start,
    duration,
    interval: sizeBytes / offeredBytesPerSec,
    sizeBytes,
  });
}
