import { mkdirSync } from 'node:fs';
import { open, rename } from 'node:fs/promises';
import type { FileHandle } from 'node:fs/promises';
import { join } from 'node:path';
import { encodeFrame, FRAME_VERSION } from '../codec/frame.ts';
import {
  decodeLogEntryView,
  encodeLogEntryPayload,
  makeEntry,
  type LogEntry,
} from '../codec/logEntry.ts';
import { ByteReader } from '../codec/reader.ts';
import { ByteWriter } from '../codec/writer.ts';
import { MappedSegment } from './mappedSegment.ts';
import { resources } from './resources.ts';

export interface Ballot {
  term: number;
  nodeId: number;
}

export const ZERO_BALLOT: Ballot = { term: 0, nodeId: 0 };

export function compareBallot(a: Ballot, b: Ballot): number {
  return a.term - b.term || a.nodeId - b.nodeId;
}

export interface LogStoreOptions {
  /** fsync after every append (default true). Disable only in benchmarks. */
  syncOnWrite?: boolean;
}

/**
 * Durable per-node Paxos log: an append-only frame segment for accepted
 * entries plus an atomically-renamed state file for the promised ballot and
 * commit index. Appends are serialized through an internal promise queue so
 * concurrent accept() calls cannot interleave bytes on disk.
 */
export class LogStore {
  #entries = new Map<number, LogEntry>();
  #fd: FileHandle | null = null;
  #queue: Promise<void> = Promise.resolve();
  #pending = 0;
  #closed = false;
  #syncOnWrite: boolean;
  #offset = 0;

  promised: Ballot = { ...ZERO_BALLOT };
  commitIndex = 0;

  readonly dir: string;

  private constructor(dir: string, options: LogStoreOptions = {}) {
    this.dir = dir;
    this.#syncOnWrite = options.syncOnWrite ?? true;
    resources.open('store');
  }

  static async open(dir: string, options: LogStoreOptions = {}): Promise<LogStore> {
    mkdirSync(dir, { recursive: true });
    const store = new LogStore(dir, options);
    await store.#load();
    return store;
  }

  get segmentPath(): string {
    return join(this.dir, 'log.seg');
  }

  get statePath(): string {
    return join(this.dir, 'state.bin');
  }

  async #load(): Promise<void> {
    // State file (best effort: absent on first boot).
    try {
      const seg = MappedSegment.map(this.statePath);
      try {
        for (const frame of seg.scan()) {
          const r = new ByteReader(frame.payload);
          this.promised = { term: r.varint(), nodeId: r.varint() };
          this.commitIndex = r.varint();
        }
      } finally {
        seg.close();
      }
    } catch (err) {
      if ((err as NodeJS.ErrnoException).code !== 'ENOENT') throw err;
    }
    // Entry segment: zero-copy scan rebuilds the in-memory index; entry
    // values stay as views into the mapped buffer (no copy on recovery).
    try {
      const seg = MappedSegment.map(this.segmentPath);
      try {
        for (const frame of seg.scan()) {
          const entry = decodeLogEntryView(frame.payload);
          this.#entries.set(entry.index, entry);
        }
        if (seg.truncatedAt >= 0) {
          // Torn tail from a crash: truncate to the last clean frame.
          const fh = await open(this.segmentPath, 'r+');
          try {
            await fh.truncate(seg.truncatedAt);
          } finally {
            await fh.close();
          }
        }
        this.#offset = seg.truncatedAt >= 0 ? seg.truncatedAt : seg.buffer.length;
      } finally {
        seg.close();
      }
    } catch (err) {
      if ((err as NodeJS.ErrnoException).code !== 'ENOENT') throw err;
      this.#offset = 0;
    }
    this.#fd = await open(this.segmentPath, 'a');
  }

  /** Serialize all mutations through one queue: concurrency-safe appends. */
  #enqueue<T>(fn: () => Promise<T>): Promise<T> {
    if (this.#closed) return Promise.reject(new Error('store is closed'));
    this.#pending++;
    const run = this.#queue.then(fn);
    this.#queue = run.then(
      () => undefined,
      () => undefined,
    );
    return run.finally(() => {
      this.#pending--;
    });
  }

  get pendingWrites(): number {
    return this.#pending;
  }

  /** Persist an accepted entry (idempotent per index: last write wins). */
  accept(entry: LogEntry): Promise<void> {
    return this.#enqueue(async () => {
      const frame = encodeFrame(encodeLogEntryPayload(entry), FRAME_VERSION);
      await this.#fd!.write(frame, 0, frame.length, this.#offset);
      this.#offset += frame.length;
      if (this.#syncOnWrite) await this.#fd!.sync();
      this.#entries.set(entry.index, entry);
    });
  }

  setPromised(ballot: Ballot): Promise<void> {
    return this.#enqueue(async () => {
      this.promised = { ...ballot };
      await this.#persistState();
    });
  }

  setCommitIndex(index: number): Promise<void> {
    return this.#enqueue(async () => {
      this.commitIndex = index;
      await this.#persistState();
    });
  }

  async #persistState(): Promise<void> {
    const w = new ByteWriter(32);
    w.varint(this.promised.term).varint(this.promised.nodeId).varint(this.commitIndex);
    const tmp = this.statePath + '.tmp';
    const fh = await open(tmp, 'w');
    try {
      await fh.write(encodeFrame(w.finish()));
      if (this.#syncOnWrite) await fh.sync();
    } finally {
      await fh.close();
    }
    await rename(tmp, this.statePath);
  }

  get(index: number): LogEntry | undefined {
    return this.#entries.get(index);
  }

  has(index: number): boolean {
    return this.#entries.has(index);
  }

  get maxIndex(): number {
    let max = 0;
    for (const i of this.#entries.keys()) if (i > max) max = i;
    return max;
  }

  /** All accepted entries with index >= from, sorted ascending. */
  entriesFrom(from: number): LogEntry[] {
    return [...this.#entries.values()].filter((e) => e.index >= from).sort((a, b) => a.index - b.index);
  }

  /** Highest index such that every slot in [1, index] is filled. */
  get contiguousIndex(): number {
    let i = 0;
    while (this.#entries.has(i + 1)) i++;
    return i;
  }

  async close(): Promise<void> {
    if (this.#closed) return;
    this.#closed = true;
    await this.#queue;
    if (this.#pending !== 0) throw new Error(`store closed with ${this.#pending} pending writes`);
    await this.#fd?.close();
    this.#fd = null;
    resources.close('store');
  }
}

export { makeEntry };
