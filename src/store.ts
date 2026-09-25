import { createReadStream, createWriteStream } from "node:fs";
import { once } from "node:events";
import * as fs from "node:fs/promises";
import * as path from "node:path";
import * as readline from "node:readline";
import {
  compareRecords,
  decodeRecord,
  encodeRecord,
  type PathRecord,
} from "./record.ts";
import type { PathInterner } from "./path.ts";

/** A record plus where it came from, so errors can be located. */
export interface SourcedRecord {
  rec: PathRecord;
  origin: string;
}

export interface RecordStoreOptions {
  interner: PathInterner;
  /** Directory for spill runs. Omit to keep everything in memory. */
  spillDir?: string;
  /** Max buffered records before a sorted run is spilled to disk. */
  maxBuffered?: number;
}

/**
 * Ingests arbitrarily chunked, arbitrarily ordered records. When the buffer
 * grows past `maxBuffered` it is sorted and spilled to disk as a run; reading
 * k-way merges the runs and the in-memory buffer, so records are processed as
 * a stream and never fully materialized before use.
 */
export class RecordStore {
  private buf: PathRecord[] = [];
  private readonly runs: string[] = [];
  private runSeq = 0;
  private readonly opts: RecordStoreOptions;

  constructor(opts: RecordStoreOptions) {
    this.opts = opts;
  }

  get spillFiles(): readonly string[] {
    return this.runs;
  }

  async add(rec: PathRecord): Promise<void> {
    this.buf.push(rec);
    const limit = this.opts.maxBuffered ?? Number.POSITIVE_INFINITY;
    if (this.buf.length >= limit) await this.flush();
  }

  async addAll(chunk: Iterable<PathRecord>): Promise<void> {
    for (const rec of chunk) await this.add(rec);
  }

  private async flush(): Promise<void> {
    if (this.opts.spillDir === undefined || this.buf.length === 0) return;
    this.buf.sort(compareRecords);
    const file = path.join(
      this.opts.spillDir,
      `run-${String(this.runSeq++).padStart(4, "0")}.jsonl`,
    );
    const ws = createWriteStream(file);
    for (const rec of this.buf) {
      if (!ws.write(encodeRecord(rec) + "\n")) await once(ws, "drain");
    }
    ws.end();
    await once(ws, "finish");
    this.runs.push(file);
    this.buf = [];
  }

  /** Single-pass sorted merge of every run and the in-memory buffer. */
  async *records(): AsyncGenerator<SourcedRecord> {
    await this.flush();
    const sources: AsyncIterator<SourcedRecord>[] = [];
    if (this.buf.length > 0) {
      const sorted = [...this.buf].sort(compareRecords);
      sources.push(arrayIterator(sorted));
    }
    for (const run of this.runs) {
      sources.push(lineIterator(run, this.opts.interner));
    }
    const heads: (SourcedRecord | null)[] = [];
    for (const it of sources) {
      const next = await it.next();
      heads.push(next.done === true ? null : next.value);
    }
    for (;;) {
      let minIdx = -1;
      for (let i = 0; i < heads.length; i++) {
        const head = heads[i];
        if (head === null) continue;
        const min = minIdx === -1 ? null : heads[minIdx];
        if (min === null || compareRecords(head.rec, min.rec) < 0) minIdx = i;
      }
      if (minIdx === -1) return;
      yield heads[minIdx] as SourcedRecord;
      const next = await sources[minIdx].next();
      heads[minIdx] = next.done === true ? null : next.value;
    }
  }

  async close(): Promise<void> {
    for (const run of this.runs) await fs.rm(run, { force: true });
    this.runs.length = 0;
  }
}

async function* arrayIterator(sorted: PathRecord[]): AsyncGenerator<SourcedRecord> {
  for (let i = 0; i < sorted.length; i++) {
    yield { rec: sorted[i], origin: `buffer#${i + 1}` };
  }
}

async function* lineIterator(
  file: string,
  interner: PathInterner,
): AsyncGenerator<SourcedRecord> {
  const rl = readline.createInterface({
    input: createReadStream(file),
    crlfDelay: Number.POSITIVE_INFINITY,
  });
  let lineNo = 0;
  for await (const line of rl) {
    lineNo++;
    if (line.length === 0) continue;
    const origin = `${file}:${lineNo}`;
    yield { rec: decodeRecord(line, interner, origin), origin };
  }
}
