import fs from "node:fs";
import path from "node:path";
import { crc32 } from "./crc.ts";
import type { LogRecord, LSN } from "./types.ts";

export class CrashError extends Error {
  constructor(message = "simulated crash: storage went down") {
    super(message);
    this.name = "CrashError";
  }
}

export interface SegmentInfo {
  file: string;
  firstLSN: LSN;
  maxLSN: LSN;
  records: number;
}

export interface TruncationResult {
  removedSegments: string[];
  retainedFrom: LSN | null;
}

/**
 * Synthetic durable file layer: append-only segment files, one JSON record per
 * line with a CRC32 trailer. Supports fault injection:
 *  - crashAfterAppends: storage dies after N more appends (CrashError)
 *  - tornNextAppend:    next append is physically half-written (torn write)
 */
export class FileLayer {
  readonly dir: string;
  private capacity: number;
  private current: string | null = null;
  private currentCount = 0;
  private tailOpen = false;
  private crashed = false;
  crashAfterAppends: number | null = null;
  tornNextAppend = false;

  constructor(dir: string, capacity = 100) {
    this.dir = dir;
    this.capacity = capacity;
    fs.mkdirSync(dir, { recursive: true });
    this.locateCurrent();
  }

  private segFiles(): string[] {
    return fs
      .readdirSync(this.dir)
      .filter((f) => /^seg-\d+\.log$/.test(f))
      .sort();
  }

  private locateCurrent(): void {
    const segs = this.segFiles();
    this.current = null;
    this.currentCount = 0;
    this.tailOpen = false;
    if (!segs.length) return;
    const last = segs[segs.length - 1];
    const content = fs.readFileSync(path.join(this.dir, last), "utf8");
    this.current = last;
    this.currentCount = content.split("\n").filter((l) => l.length > 0).length;
    this.tailOpen = content.length > 0 && !content.endsWith("\n");
  }

  private roll(firstLSN: LSN): void {
    this.current = `seg-${String(firstLSN).padStart(12, "0")}.log`;
    this.currentCount = 0;
    this.tailOpen = false;
  }

  append(rec: LogRecord): void {
    if (this.crashed) throw new CrashError();
    if (this.crashAfterAppends !== null) {
      if (this.crashAfterAppends <= 0) {
        this.crashed = true;
        throw new CrashError("simulated crash: storage died before append");
      }
      this.crashAfterAppends--;
    }
    const body = JSON.stringify(rec);
    let line = `${body}|${crc32(body)}\n`;
    if (this.tornNextAppend) {
      this.tornNextAppend = false;
      line = line.slice(0, Math.max(1, line.length >> 1));
    }
    if (this.current === null || this.currentCount >= this.capacity) this.roll(rec.lsn);
    const p = path.join(this.dir, this.current);
    if (this.tailOpen) {
      fs.appendFileSync(p, "\n");
      this.tailOpen = false;
    }
    fs.appendFileSync(p, line);
    this.tailOpen = !line.endsWith("\n");
    this.currentCount++;
  }

  revive(): void {
    this.crashed = false;
    this.crashAfterAppends = null;
    this.locateCurrent();
  }

  readAll(): { records: LogRecord[]; anomalies: string[] } {
    const records: LogRecord[] = [];
    const anomalies: string[] = [];
    for (const f of this.segFiles()) {
      const lines = fs.readFileSync(path.join(this.dir, f), "utf8").split("\n");
      for (const line of lines) {
        if (!line) continue;
        const sep = line.lastIndexOf("|");
        let ok = false;
        if (sep > 0) {
          const body = line.slice(0, sep);
          if (crc32(body) === line.slice(sep + 1)) {
            try {
              records.push(JSON.parse(body) as LogRecord);
              ok = true;
            } catch {
              ok = false;
            }
          }
        }
        if (!ok) anomalies.push(`${f}: dropped torn/corrupt record (${line.length} bytes)`);
      }
    }
    records.sort((a, b) => a.lsn - b.lsn);
    return { records, anomalies };
  }

  segments(): SegmentInfo[] {
    return this.segFiles().map((f) => {
      const first = Number(f.slice(4, -4));
      let max = first;
      let records = 0;
      const lines = fs.readFileSync(path.join(this.dir, f), "utf8").split("\n");
      for (const line of lines) {
        if (!line) continue;
        const sep = line.lastIndexOf("|");
        if (sep <= 0) continue;
        const body = line.slice(0, sep);
        if (crc32(body) !== line.slice(sep + 1)) continue;
        try {
          const rec = JSON.parse(body) as LogRecord;
          max = Math.max(max, rec.lsn);
          records++;
        } catch {
          /* torn line */
        }
      }
      return { file: f, firstLSN: first, maxLSN: max, records };
    });
  }

  /** Drop whole segments whose newest record is older than `boundary`. */
  truncateBelow(boundary: LSN): TruncationResult {
    const removed: string[] = [];
    for (const info of this.segments()) {
      if (info.maxLSN < boundary) {
        fs.unlinkSync(path.join(this.dir, info.file));
        removed.push(info.file);
      }
    }
    this.locateCurrent();
    const remaining = this.segFiles();
    return {
      removedSegments: removed,
      retainedFrom: remaining.length ? Number(remaining[0].slice(4, -4)) : null,
    };
  }
}
