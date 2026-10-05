import { createHash } from 'node:crypto';
import { promises as fs } from 'node:fs';
import type { FileHandle } from 'node:fs/promises';
import * as path from 'node:path';
import { IntervalSet } from './intervals.ts';

export type VersionState = 'receiving' | 'quarantined' | 'published' | 'failed';

export type AddSegmentResult =
  | 'stored'
  | 'duplicate'
  | 'published'
  | 'quarantined-conflict'
  | 'failed-digest'
  | 'rejected-out-of-bounds'
  | 'rejected-not-receiving'
  | 'rejected-unknown-version';

export interface StoreOptions {
  /** Hard cap on bytes held in memory before spilling to disk. Default 8 MiB. */
  maxBufferedBytes?: number;
}

export interface VersionInfo {
  state: VersionState;
  size: number;
  sha256: string;
  receivedBytes: number;
  gaps: Array<[number, number]>;
}

interface VersionMeta {
  size: number;
  sha256: string;
  state: VersionState;
  /** Ranges whose bytes are written to the data file AND fsynced. Only these go into checkpoints. */
  persisted: IntervalSet;
}

const DEFAULT_MAX_BUFFERED = 8 * 1024 * 1024;
const CHECKPOINT_FILE = 'checkpoint.json';

function encodeId(fileId: string): string {
  return Buffer.from(fileId, 'utf8').toString('hex');
}

async function fsyncDir(dir: string): Promise<void> {
  const handle = await fs.open(dir, 'r');
  try {
    await handle.sync();
  } finally {
    await handle.close();
  }
}

async function pathExists(p: string): Promise<boolean> {
  try {
    await fs.access(p);
    return true;
  } catch {
    return false;
  }
}

class VersionAssembly {
  readonly fileId: string;
  readonly version: number;
  meta: VersionMeta;
  /** Ranges received in this process: persisted ∪ dirty. */
  private received = new IntervalSet();
  /** Bytes accepted into memory but not yet fsynced. Keyed by chunk offset. */
  private dirty = new Map<number, Buffer>();
  private dirtyBytes = 0;
  private readonly dataPath: string;

  constructor(
    fileId: string,
    version: number,
    meta: VersionMeta,
    dataPath: string,
  ) {
    this.fileId = fileId;
    this.version = version;
    this.meta = meta;
    this.dataPath = dataPath;
    this.received = IntervalSet.fromJSON(meta.persisted.toJSON());
  }

  get state(): VersionState {
    return this.meta.state;
  }

  get bufferedBytes(): number {
    return this.dirtyBytes;
  }

  get receivedSet(): IntervalSet {
    return this.received;
  }

  /** Read previously accepted bytes in [start, end). Dirty bytes take precedence over disk. */
  private async readExisting(start: number, end: number): Promise<Buffer> {
    const out = Buffer.alloc(end - start);
    if (await pathExists(this.dataPath)) {
      const handle = await fs.open(this.dataPath, 'r');
      try {
        let pos = start;
        while (pos < end) {
          const { bytesRead } = await handle.read(out, pos - start, end - pos, pos);
          if (bytesRead === 0) break;
          pos += bytesRead;
        }
      } finally {
        await handle.close();
      }
    }
    for (const [offset, chunk] of this.dirty) {
      const s = Math.max(offset, start);
      const e = Math.min(offset + chunk.length, end);
      if (s < e) chunk.copy(out, s - start, s - offset, e - offset);
    }
    return out;
  }

  /** Returns true if every overlapping byte already received matches the incoming data. */
  async overlapsAreConsistent(offset: number, data: Buffer): Promise<boolean> {
    for (const [s, e] of this.received.overlaps(offset, offset + data.length)) {
      const existing = await this.readExisting(s, e);
      if (!existing.equals(data.subarray(s - offset, e - offset))) return false;
    }
    return true;
  }

  accept(offset: number, data: Buffer): void {
    this.dirty.set(offset, data);
    this.dirtyBytes += data.length;
    this.received.add(offset, offset + data.length);
  }

  /**
   * Write a chunk straight to disk (used when a single segment exceeds the memory budget).
   * Data is fsynced before the range is marked persisted.
   */
  async writeThrough(offset: number, data: Buffer): Promise<void> {
    const handle = await fs.open(this.dataPath, 'a+');
    try {
      await handle.write(data, 0, data.length, offset);
      await handle.sync();
    } finally {
      await handle.close();
    }
    this.meta.persisted.add(offset, offset + data.length);
    this.received.add(offset, offset + data.length);
  }

  /** Flush dirty bytes to the data file and fsync. Afterwards persisted == received. */
  async flushData(): Promise<void> {
    if (this.dirtyBytes > 0) {
      const handle = await fs.open(this.dataPath, 'a+');
      try {
        for (const [offset, chunk] of [...this.dirty.entries()].sort((a, b) => a[0] - b[0])) {
          await handle.write(chunk, 0, chunk.length, offset);
        }
        await handle.sync();
      } finally {
        await handle.close();
      }
      for (const [offset, chunk] of this.dirty) {
        this.meta.persisted.add(offset, offset + chunk.length);
      }
      this.dirty.clear();
      this.dirtyBytes = 0;
    }
  }

  async digest(): Promise<string> {
    const hash = createHash('sha256');
    const handle = await fs.open(this.dataPath, 'r');
    try {
      const stat = await handle.stat();
      const size = Math.min(stat.size, this.meta.size);
      const chunkSize = 1024 * 1024;
      const buf = Buffer.allocUnsafe(Math.min(chunkSize, Math.max(size, 1)));
      let pos = 0;
      while (pos < size) {
        const want = Math.min(buf.length, size - pos);
        const { bytesRead } = await handle.read(buf, 0, want, pos);
        if (bytesRead === 0) break;
        hash.update(buf.subarray(0, bytesRead));
        pos += bytesRead;
      }
      // Pad a short/sparse file deterministically: digest is computed over exactly size bytes.
      if (pos < size) {
        const zeros = Buffer.alloc(Math.min(chunkSize, size - pos));
        let remaining = size - pos;
        while (remaining > 0) {
          const n = Math.min(remaining, zeros.length);
          hash.update(zeros.subarray(0, n));
          remaining -= n;
        }
      }
      return hash.digest('hex');
    } finally {
      await handle.close();
    }
  }

  async discardData(): Promise<void> {
    this.dirty.clear();
    this.dirtyBytes = 0;
    await fs.rm(this.dataPath, { force: true });
  }
}

interface CheckpointShape {
  format: 1;
  files: Record<
    string,
    {
      versions: Record<
        string,
        { size: number; sha256: string; state: VersionState; intervals: Array<[number, number]> }
      >;
    }
  >;
}

export class ReassemblyStore {
  private readonly dir: string;
  private readonly dataDir: string;
  private readonly publishedDir: string;
  private readonly maxBufferedBytes: number;
  private files = new Map<string, Map<number, VersionAssembly>>();
  private queue: Promise<unknown> = Promise.resolve();

  private constructor(dir: string, maxBufferedBytes: number) {
    this.dir = dir;
    this.dataDir = path.join(dir, 'data');
    this.publishedDir = path.join(dir, 'published');
    this.maxBufferedBytes = maxBufferedBytes;
  }

  /** Open (or create) a store, recovering any checkpointed state. */
  static async open(dir: string, options: StoreOptions = {}): Promise<ReassemblyStore> {
    const store = new ReassemblyStore(dir, options.maxBufferedBytes ?? DEFAULT_MAX_BUFFERED);
    await fs.mkdir(store.dataDir, { recursive: true });
    await fs.mkdir(store.publishedDir, { recursive: true });
    await store.recover();
    return store;
  }

  private dataPath(fileId: string, version: number): string {
    return path.join(this.dataDir, `${encodeId(fileId)}.${version}.part`);
  }

  /** Final published payload for a file (content of the most recently published version). */
  publishedPath(fileId: string): string {
    return path.join(this.publishedDir, encodeId(fileId));
  }

  private async recover(): Promise<void> {
    let raw: string;
    try {
      raw = await fs.readFile(path.join(this.dir, CHECKPOINT_FILE), 'utf8');
    } catch {
      return; // No checkpoint: brand-new store.
    }
    const checkpoint = JSON.parse(raw) as CheckpointShape;
    for (const [fileId, fileEntry] of Object.entries(checkpoint.files)) {
      let versions = this.files.get(fileId);
      if (!versions) {
        versions = new Map();
        this.files.set(fileId, versions);
      }
      for (const [versionText, v] of Object.entries(fileEntry.versions)) {
        const version = Number(versionText);
        const persisted = IntervalSet.fromJSON(v.intervals);
        // Defensive: checkpoint promises these bytes were fsynced. If the data file is
        // missing anyway, do not trust the intervals — drop back to gaps.
        const effective =
          v.state === 'receiving' && persisted.coveredLength() > 0 &&
          !(await pathExists(this.dataPath(fileId, version)))
            ? new IntervalSet()
            : persisted;
        versions.set(
          version,
          new VersionAssembly(fileId, version, {
            size: v.size,
            sha256: v.sha256,
            state: v.state,
            persisted: effective,
          }, this.dataPath(fileId, version)),
        );
      }
    }
  }

  /** Serialize all mutating operations so async disk work cannot interleave. */
  private enqueue<T>(fn: () => Promise<T>): Promise<T> {
    const run = this.queue.then(fn);
    this.queue = run.catch(() => undefined);
    return run;
  }

  /**
   * Declare an expected file version. Idempotent for identical parameters.
   * A zero-size file is verified and published (or failed) immediately.
   */
  async declareFile(fileId: string, version: number, size: number, sha256: string): Promise<VersionState> {
    return this.enqueue(async () => {
      if (!Number.isSafeInteger(size) || size < 0) throw new Error(`invalid size: ${size}`);
      if (!Number.isSafeInteger(version) || version < 0) throw new Error(`invalid version: ${version}`);
      let versions = this.files.get(fileId);
      if (!versions) {
        versions = new Map();
        this.files.set(fileId, versions);
      }
      const existing = versions.get(version);
      if (existing) {
        if (existing.meta.size !== size || existing.meta.sha256 !== sha256) {
          throw new Error(`conflicting declaration for ${fileId}@${version}`);
        }
        return existing.state;
      }
      const assembly = new VersionAssembly(fileId, version, {
        size,
        sha256,
        state: 'receiving',
        persisted: new IntervalSet(),
      }, this.dataPath(fileId, version));
      versions.set(version, assembly);
      if (size === 0) {
        await this.finalizeEmpty(assembly);
      }
      await this.writeCheckpoint();
      return assembly.state;
    });
  }

  private async finalizeEmpty(assembly: VersionAssembly): Promise<void> {
    const emptyDigest = createHash('sha256').update(Buffer.alloc(0)).digest('hex');
    if (emptyDigest === assembly.meta.sha256) {
      await this.publish(assembly);
    } else {
      assembly.meta.state = 'failed';
    }
  }

  async addSegment(
    fileId: string,
    version: number,
    offset: number,
    data: Buffer | Uint8Array,
  ): Promise<AddSegmentResult> {
    const chunk = Buffer.isBuffer(data) ? Buffer.from(data) : Buffer.from(data.buffer, data.byteOffset, data.byteLength);
    return this.enqueue(async () => {
      const assembly = this.files.get(fileId)?.get(version);
      if (!assembly) return 'rejected-unknown-version';
      if (assembly.state !== 'receiving') return 'rejected-not-receiving';
      const size = assembly.meta.size;
      if (!Number.isSafeInteger(offset) || offset < 0 || offset > size || offset + chunk.length > size) {
        return 'rejected-out-of-bounds';
      }
      if (chunk.length === 0) return 'duplicate';

      if (!(await assembly.overlapsAreConsistent(offset, chunk))) {
        assembly.meta.state = 'quarantined';
        await assembly.discardData();
        await this.writeCheckpoint();
        return 'quarantined-conflict';
      }

      const alreadyCovered = assembly.receivedSet.overlaps(offset, offset + chunk.length)
        .reduce((n, [s, e]) => n + (e - s), 0) === chunk.length;
      if (alreadyCovered) return 'duplicate';

      if (chunk.length >= this.maxBufferedBytes) {
        // Too large to buffer: go straight to disk, fsync, then it is checkpoint-safe.
        await this.flushAllData();
        await assembly.writeThrough(offset, chunk);
        await this.writeCheckpoint();
      } else {
        if (this.bufferedBytes() + chunk.length > this.maxBufferedBytes) {
          await this.flushAllData();
          await this.writeCheckpoint();
        }
        assembly.accept(offset, chunk);
      }

      if (assembly.receivedSet.isComplete(size)) {
        return this.finalize(assembly);
      }
      return 'stored';
    });
  }

  /** Flush all dirty data (fsync) and write a checkpoint. Safe recovery point. */
  async flush(): Promise<void> {
    return this.enqueue(async () => {
      await this.flushAllData();
      await this.writeCheckpoint();
    });
  }

  private async finalize(assembly: VersionAssembly): Promise<AddSegmentResult> {
    // Everything must be durable before we trust coverage for the digest.
    await this.flushAllData();
    const digest = await assembly.digest();
    if (digest !== assembly.meta.sha256) {
      assembly.meta.state = 'failed';
      await this.writeCheckpoint();
      return 'failed-digest';
    }
    await this.publish(assembly);
    await this.writeCheckpoint();
    return 'published';
  }

  private async publish(assembly: VersionAssembly): Promise<void> {
    const target = this.publishedPath(assembly.fileId);
    if (assembly.meta.size === 0) {
      await fs.writeFile(target, Buffer.alloc(0));
    } else {
      await fs.rename(this.dataPath(assembly.fileId, assembly.version), target);
    }
    assembly.meta.state = 'published';
    await fsyncDir(this.publishedDir);
  }

  private bufferedBytes(): number {
    let total = 0;
    for (const versions of this.files.values()) {
      for (const assembly of versions.values()) total += assembly.bufferedBytes;
    }
    return total;
  }

  private async flushAllData(): Promise<void> {
    for (const versions of this.files.values()) {
      for (const assembly of versions.values()) {
        if (assembly.state === 'receiving') await assembly.flushData();
      }
    }
  }

  /** Checkpoint only ever records fsynced ranges, never dirty in-memory bytes. */
  private async writeCheckpoint(): Promise<void> {
    const files: CheckpointShape['files'] = {};
    for (const [fileId, versions] of this.files) {
      const entry: Record<string, { size: number; sha256: string; state: VersionState; intervals: Array<[number, number]> }> = {};
      for (const [version, assembly] of versions) {
        entry[String(version)] = {
          size: assembly.meta.size,
          sha256: assembly.meta.sha256,
          state: assembly.meta.state,
          intervals: assembly.meta.persisted.toJSON(),
        };
      }
      files[fileId] = { versions: entry };
    }
    const checkpoint: CheckpointShape = { format: 1, files };
    const tmp = path.join(this.dir, `${CHECKPOINT_FILE}.tmp`);
    const handle: FileHandle = await fs.open(tmp, 'w');
    try {
      await handle.writeFile(JSON.stringify(checkpoint));
      await handle.sync();
    } finally {
      await handle.close();
    }
    await fs.rename(tmp, path.join(this.dir, CHECKPOINT_FILE));
    await fsyncDir(this.dir);
  }

  getVersionInfo(fileId: string, version: number): VersionInfo | undefined {
    const assembly = this.files.get(fileId)?.get(version);
    if (!assembly) return undefined;
    return {
      state: assembly.meta.state,
      size: assembly.meta.size,
      sha256: assembly.meta.sha256,
      receivedBytes: assembly.receivedSet.coveredLength(),
      gaps: assembly.receivedSet.gaps(assembly.meta.size),
    };
  }

  listVersions(fileId: string): number[] {
    const versions = this.files.get(fileId);
    return versions ? [...versions.keys()].sort((a, b) => a - b) : [];
  }

  stats(): { bufferedBytes: number; maxBufferedBytes: number } {
    return { bufferedBytes: this.bufferedBytes(), maxBufferedBytes: this.maxBufferedBytes };
  }
}
