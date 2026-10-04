import * as fs from "node:fs";
import * as path from "node:path";
import { NULL_LSN } from "./types.ts";
import type { CkptEndRecord, LSN, LogRecord } from "./types.ts";
import { SegmentStore } from "./fileLayer.ts";

export interface MetaFile {
  firstAvailableLSN: LSN;
  lastTruncation: { boundary: LSN; reasons: string[]; at: string } | null;
}

export class LogManager {
  readonly store: SegmentStore;
  readonly metaPath: string;
  meta: MetaFile;

  private records: LogRecord[] = []; // whole retained log, in memory
  private byLSN = new Map<LSN, LogRecord>();
  /** undecodable tail bytes seen while scanning (torn writes) */
  tornTailBytes = 0;
  private nextLSN: LSN = 1;
  private currentSegmentFirstLSN: LSN = 1;
  private currentSegmentCount = 0;

  constructor(dir: string, maxRecordsPerSegment = 100) {
    this.store = new SegmentStore(dir, maxRecordsPerSegment);
    this.metaPath = path.join(dir, "meta.json");
    this.meta = this.loadMeta();
    this.scan();
  }

  private loadMeta(): MetaFile {
    try {
      return JSON.parse(fs.readFileSync(this.metaPath, "utf8")) as MetaFile;
    } catch {
      return { firstAvailableLSN: 1, lastTruncation: null };
    }
  }

  private saveMeta(): void {
    const tmp = this.metaPath + ".tmp";
    fs.writeFileSync(tmp, JSON.stringify(this.meta, null, 2));
    fs.renameSync(tmp, this.metaPath);
  }

  private scan(): void {
    let maxLSN = NULL_LSN;
    this.tornTailBytes = 0;
    for (const file of this.store.listSegments()) {
      const { payloads, trailingBytes } = this.store.readSegment(file);
      this.tornTailBytes += trailingBytes;
      for (const payload of payloads) {
        const rec = JSON.parse(payload.toString("utf8")) as LogRecord;
        if (rec.lsn < this.meta.firstAvailableLSN) continue;
        this.records.push(rec);
        this.byLSN.set(rec.lsn, rec);
        if (rec.lsn > maxLSN) maxLSN = rec.lsn;
      }
    }
    this.nextLSN = maxLSN + 1;
    const segs = this.store.listSegments();
    if (segs.length === 0) {
      this.currentSegmentFirstLSN = this.nextLSN;
      this.currentSegmentCount = 0;
    } else {
      const last = segs[segs.length - 1];
      const count = this.store.readSegment(last).payloads.length;
      this.currentSegmentFirstLSN = parseInt(last.slice(4, 16), 10);
      this.currentSegmentCount = count;
      if (count >= this.store.maxRecordsPerSegment) {
        this.currentSegmentFirstLSN = this.nextLSN;
        this.currentSegmentCount = 0;
      }
    }
  }

  get firstAvailableLSN(): LSN {
    return this.meta.firstAvailableLSN;
  }

  get highWaterLSN(): LSN {
    return this.nextLSN - 1;
  }

  allocLSN(): LSN {
    return this.nextLSN;
  }

  append(rec: LogRecord): LSN {
    if (this.currentSegmentCount >= this.store.maxRecordsPerSegment) {
      this.currentSegmentFirstLSN = this.nextLSN;
      this.currentSegmentCount = 0;
    }
    const payload = Buffer.from(JSON.stringify(rec), "utf8");
    this.store.appendFrame(this.currentSegmentFirstLSN, payload);
    this.currentSegmentCount++;
    this.records.push(rec);
    this.byLSN.set(rec.lsn, rec);
    if (rec.lsn >= this.nextLSN) this.nextLSN = rec.lsn + 1;
    return rec.lsn;
  }

  read(lsn: LSN): LogRecord {
    const rec = this.byLSN.get(lsn);
    if (!rec) throw new Error(`LSN ${lsn} not found in retained log`);
    return rec;
  }

  all(): LogRecord[] {
    return this.records;
  }

  /** Last checkpoint whose CKPT_BEGIN and matching CKPT_END both survived. */
  lastCompleteCheckpoint(): { beginLSN: LSN; end: CkptEndRecord } | null {
    const begins = new Map<number, LSN>();
    let best: { beginLSN: LSN; end: CkptEndRecord } | null = null;
    for (const rec of this.records) {
      if (rec.type === "CKPT_BEGIN") begins.set(rec.ckptId, rec.lsn);
      else if (rec.type === "CKPT_END" && begins.has(rec.ckptId)) {
        best = { beginLSN: begins.get(rec.ckptId)!, end: rec };
      }
    }
    return best;
  }

  /** Physically drop whole segments whose newest record is below `boundary`. */
  truncate(boundary: LSN, reasons: string[]): { removed: string[] } {
    const removed: string[] = [];
    const segs = this.store.listSegments();
    for (let i = 0; i < segs.length; i++) {
      const file = segs[i];
      const { payloads } = this.store.readSegment(file);
      if (payloads.length === 0) continue;
      const last = JSON.parse(
        payloads[payloads.length - 1].toString("utf8"),
      ) as LogRecord;
      const isCurrent = i === segs.length - 1;
      if (!isCurrent && last.lsn < boundary) {
        this.store.deleteSegment(file);
        removed.push(file);
      }
    }
    if (removed.length > 0) {
      this.meta.firstAvailableLSN = Math.max(this.meta.firstAvailableLSN, boundary);
      this.meta.lastTruncation = {
        boundary,
        reasons,
        at: new Date().toISOString(),
      };
      this.saveMeta();
      this.records = [];
      this.byLSN = new Map();
      const keepNext = this.nextLSN;
      this.scan();
      this.nextLSN = Math.max(this.nextLSN, keepNext);
    }
    return { removed };
  }
}
