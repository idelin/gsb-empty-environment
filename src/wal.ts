import { createHash } from "node:crypto";
import { appendFileSync, closeSync, existsSync, fsyncSync, openSync, readFileSync } from "node:fs";

export interface WalOp {
  cf: string;
  key: string;
  value: string | null; // null = delete
}

export interface WalBatch {
  seq: number;
  ops: WalOp[];
}

function checksum(payload: string): string {
  return createHash("sha256").update(payload).digest("hex").slice(0, 16);
}

/**
 * One physical log shared by every column family.
 * A multi-CF write is encoded as a SINGLE line record, so a batch is
 * either fully replayed or fully ignored during recovery (atomicity).
 */
export class Wal {
  private readonly path: string;
  private readonly fd: number;

  constructor(path: string) {
    this.path = path;
    this.fd = openSync(path, "a");
  }

  /** Append one atomic batch (possibly spanning multiple CFs) and fsync. */
  appendBatch(batch: WalBatch): void {
    const payload = JSON.stringify(batch);
    const line = `${payload.length}:${checksum(payload)}:${payload}\n`;
    appendFileSync(this.fd, line);
    fsyncSync(this.fd);
  }

  close(): void {
    closeSync(this.fd);
  }

  /**
   * Replay all intact batches in seq order.
   * Torn/corrupt tail records (crash mid-write) are detected via the
   * length+checksum framing and skipped, preserving atomic alignment.
   */
  static replay(path: string): WalBatch[] {
    if (!existsSync(path)) return [];
    const content = readFileSync(path, "utf8");
    const batches: WalBatch[] = [];
    for (const line of content.split("\n")) {
      if (line === "") continue;
      const first = line.indexOf(":");
      const second = line.indexOf(":", first + 1);
      if (first <= 0 || second <= 0) continue; // torn header
      const len = Number(line.slice(0, first));
      const sum = line.slice(first + 1, second);
      const payload = line.slice(second + 1);
      if (!Number.isInteger(len) || payload.length !== len) continue; // torn payload
      if (checksum(payload) !== sum) continue; // corrupt payload
      batches.push(JSON.parse(payload) as WalBatch);
    }
    return batches.sort((a, b) => a.seq - b.seq);
  }
}
