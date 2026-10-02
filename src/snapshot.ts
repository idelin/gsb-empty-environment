import type { TransferMessage } from "./messages.ts";

/** Recorded state of one incoming channel: transfers seen after local
 *  state was recorded and before the channel's marker arrived. */
export interface ChannelStateRecord {
  from: string;
  inFlight: TransferMessage[];
}

/** One process's piece of the global snapshot. */
export interface ProcessSnapshotRecord {
  processId: string;
  snapshotId: string;
  recordedAt: number;
  balance: number;
  channels: ChannelStateRecord[];
}

/** Assembled global snapshot: a consistent cut of the whole system. */
export interface GlobalSnapshot {
  snapshotId: string;
  processes: ProcessSnapshotRecord[];
}

export function inFlightTotal(record: ProcessSnapshotRecord): number {
  let sum = 0;
  for (const channel of record.channels) {
    for (const msg of channel.inFlight) sum += msg.amount;
  }
  return sum;
}

/** Total assets captured by the cut: recorded balances + in-flight transfers. */
export function snapshotTotal(snapshot: GlobalSnapshot): number {
  let sum = 0;
  for (const record of snapshot.processes) {
    sum += record.balance + inFlightTotal(record);
  }
  return sum;
}
