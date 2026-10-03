import type { FaultInjector } from './faults.ts';
import type { LogRecord, LSN } from './types.ts';
import { decodeRecords, encodeRecord } from './wal.ts';

export interface Packet {
  lsn: LSN;
  line: string;
}

/**
 * Receiving replica: accepts records in LSN order, deduplicates retransmits
 * (lost acks) and rejects corrupt packets. Self-heals purely from the
 * sender's retransmission; no persistent state of its own is required.
 */
export class ReplicaReceiver {
  readonly received: LogRecord[] = [];
  duplicates = 0;
  rejected = 0;
  private expected: LSN | null = null;

  /** Returns an ack: true when the packet was consumed (or is a duplicate). */
  deliver(packet: Packet): boolean {
    if (this.expected !== null && packet.lsn < this.expected) {
      this.duplicates++;
      return true; // duplicate from a lost ack: ack again, drop data
    }
    if (this.expected !== null && packet.lsn > this.expected) {
      this.rejected++;
      return false; // gap: ask for retransmission
    }
    const rec = decodeRecords([packet.line])[0];
    if (!rec || rec.lsn !== packet.lsn) {
      this.rejected++;
      return false; // corrupt packet
    }
    this.received.push(rec);
    this.expected = rec.lsn + 1;
    return true;
  }
}

/** Unreliable network: may drop data packets and acks. */
export class LossyChannel {
  private readonly injector: FaultInjector;
  droppedData = 0;
  droppedAcks = 0;

  constructor(injector: FaultInjector) {
    this.injector = injector;
  }

  transmit(packet: Packet, receiver: ReplicaReceiver): boolean {
    if (this.injector.check('channel.send')) {
      this.droppedData++;
      return false;
    }
    const ack = receiver.deliver(packet);
    if (!ack) return false;
    if (this.injector.check('channel.ack')) {
      this.droppedAcks++;
      return false;
    }
    return true;
  }
}

/**
 * Ships WAL records to a replica with stop-and-wait ARQ: every record is
 * retransmitted until acked, so the replica converges to the exact source
 * log despite packet loss. Holds no timers or sockets — nothing to leak.
 */
export class LogShipper {
  private readonly channel: LossyChannel;
  private readonly receiver: ReplicaReceiver;
  private readonly maxRetries: number;
  retries = 0;

  constructor(channel: LossyChannel, receiver: ReplicaReceiver, maxRetries = 500) {
    this.channel = channel;
    this.receiver = receiver;
    this.maxRetries = maxRetries;
  }

  ship(records: LogRecord[]): void {
    for (const rec of records) {
      const packet: Packet = { lsn: rec.lsn, line: encodeRecord(rec) };
      let attempt = 0;
      let done = false;
      while (!done) {
        if (attempt > 0) this.retries++;
        if (attempt++ >= this.maxRetries) {
          throw new Error(`replication of LSN ${rec.lsn} failed after ${this.maxRetries} attempts`);
        }
        done = this.channel.transmit(packet, this.receiver);
      }
    }
  }
}
