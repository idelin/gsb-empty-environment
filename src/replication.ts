import { verifyChecksum } from './checksum.ts';
import { SimulatedDisk } from './disk.ts';
import { cloneRecord } from './types.ts';
import type { LogRecord, LSN } from './types.ts';

/** Deterministic PRNG (mulberry32) for reproducible packet-loss patterns. */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/**
 * Unreliable log-shipping channel. Drops whole records (packet loss),
 * optionally with a scripted drop set or a seeded random loss rate.
 */
export class LossyChannel {
  private scriptedDrops = new Set<LSN>();
  private lossRate = 0;
  private rng: () => number = () => 0;

  dropLSNs(lsns: Iterable<LSN>): void {
    for (const lsn of lsns) this.scriptedDrops.add(lsn);
  }

  setLossRate(rate: number, seed = 42): void {
    this.lossRate = rate;
    this.rng = mulberry32(seed);
  }

  /** Ships records; returns only those that survived the network. */
  ship(records: LogRecord[]): LogRecord[] {
    return records.filter((rec) => {
      if (this.scriptedDrops.has(rec.lsn)) return false;
      if (this.lossRate > 0 && this.rng() < this.lossRate) return false;
      return true;
    });
  }
}

/**
 * Log-shipping replica. Accepts only the contiguous, checksum-valid prefix of
 * the stream: a lost packet creates an LSN gap, and everything after the gap
 * is discarded (self-healing truncation), exactly like a real WAL receiver.
 */
export class Replica {
  readonly disk = new SimulatedDisk();
  private expectedLSN: LSN = 1;
  private gapDetected = false;

  receive(records: LogRecord[]): void {
    for (const rec of records) {
      if (this.gapDetected) continue;
      if (rec.lsn !== this.expectedLSN || !verifyChecksum(rec)) {
        this.gapDetected = true;
        continue;
      }
      this.disk.appendLog(cloneRecord(rec));
      this.expectedLSN = rec.lsn + 1;
    }
  }

  get contiguousLSN(): LSN {
    return this.expectedLSN - 1;
  }

  get hasGap(): boolean {
    return this.gapDetected;
  }
}
