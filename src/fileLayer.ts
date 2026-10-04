// Synthetic durable file layer with fault injection.
// Every append is synchronously durable (direct-IO style) unless a fault is armed.
// Frame format per record: [u32 LE length][payload bytes][u32 LE crc32(payload)].
// A torn append persists only a prefix of the frame; readers stop at the first
// undecodable frame, so a half-written tail (e.g. half-written checkpoint) is
// simply ignored by recovery.

import * as fs from "node:fs";
import * as path from "node:path";

export class CrashError extends Error {
  constructor(message = "simulated crash: process died, volatile state lost") {
    super(message);
    this.name = "CrashError";
  }
}

const CRC_TABLE = (() => {
  const table = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    table[n] = c >>> 0;
  }
  return table;
})();

export function crc32(buf: Buffer): number {
  let crc = 0xffffffff;
  for (let i = 0; i < buf.length; i++) {
    crc = CRC_TABLE[(crc ^ buf[i]) & 0xff] ^ (crc >>> 8);
  }
  return (crc ^ 0xffffffff) >>> 0;
}

export interface SegmentInfo {
  file: string;
  firstLSN: number;
  lastLSN: number;
  recordCount: number;
}

export class SegmentStore {
  readonly dir: string;
  readonly maxRecordsPerSegment: number;

  // ---- fault injection state ----
  private crashAfterAppends: number | null = null;
  private tornNextAppend = false;
  private crashed = false;

  constructor(dir: string, maxRecordsPerSegment = 100) {
    this.dir = dir;
    this.maxRecordsPerSegment = maxRecordsPerSegment;
    fs.mkdirSync(dir, { recursive: true });
  }

  // ---- fault injection API (test/demo only) ----
  armCrashAfterAppends(n: number): void {
    this.crashAfterAppends = n;
  }
  armTornNextAppend(): void {
    this.tornNextAppend = true;
  }
  resetFaults(): void {
    this.crashAfterAppends = null;
    this.tornNextAppend = false;
    this.crashed = false;
  }

  private checkCrash(): void {
    if (this.crashed) throw new CrashError();
    if (this.crashAfterAppends !== null) {
      if (this.crashAfterAppends <= 0) {
        this.crashed = true;
        throw new CrashError();
      }
      this.crashAfterAppends--;
    }
  }

  private segmentPath(firstLSN: number): string {
    return path.join(this.dir, `seg-${String(firstLSN).padStart(12, "0")}.log`);
  }

  listSegments(): string[] {
    return fs
      .readdirSync(this.dir)
      .filter((f) => /^seg-\d+\.log$/.test(f))
      .sort();
  }

  /** Append one framed payload. Returns nothing; durability is synchronous. */
  appendFrame(firstLSNOfSegment: number, payload: Buffer): void {
    this.checkCrash();
    const frame = Buffer.alloc(8 + payload.length);
    frame.writeUInt32LE(payload.length, 0);
    payload.copy(frame, 4);
    frame.writeUInt32LE(crc32(payload), 4 + payload.length);

    let bytes: Buffer = frame;
    if (this.tornNextAppend) {
      this.tornNextAppend = false;
      bytes = frame.subarray(0, Math.max(1, Math.floor(frame.length / 2)));
    }
    fs.appendFileSync(this.segmentPath(firstLSNOfSegment), bytes);
  }

  /**
   * Decode all whole frames in a segment file; stops at the first torn/corrupt
   * tail. `trailingBytes` reports how many undecodable bytes were left over
   * (non-zero => a torn write, e.g. a half-written checkpoint, was detected).
   */
  readSegment(file: string): { payloads: Buffer[]; trailingBytes: number } {
    const data = fs.readFileSync(path.join(this.dir, file));
    const out: Buffer[] = [];
    let off = 0;
    while (off + 8 <= data.length) {
      const len = data.readUInt32LE(off);
      if (off + 8 + len > data.length) break; // torn tail
      const payload = data.subarray(off + 4, off + 4 + len);
      const crc = data.readUInt32LE(off + 4 + len);
      if (crc32(payload) !== crc) break; // corrupt tail
      out.push(Buffer.from(payload));
      off += 8 + len;
    }
    return { payloads: out, trailingBytes: data.length - off };
  }

  deleteSegment(file: string): void {
    fs.unlinkSync(path.join(this.dir, file));
  }
}
