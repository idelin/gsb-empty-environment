import { promises as fs } from 'node:fs';
import type { FileHandle } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { basename, join } from 'node:path';
import { IntervalSet } from './intervals.ts';
import type { IntervalTuple } from './intervals.ts';
import type { ByteBudget } from './budget.ts';
import { fsyncDirectory, pathExists, safeName, writeFileAtomic } from './storage.ts';
import {
  SegmentConflictError,
  SegmentOutOfBoundsError,
  VersionNotWritableError,
} from './errors.ts';

export interface DigestSpec {
  algorithm: string;
  hex: string;
}

export interface FileVersionSpec {
  fileId: string;
  version: string;
  /** Expected total size in bytes; segments outside [0, size) are rejected. */
  size: number;
  /** Expected whole-file digest, verified after coverage is complete. */
  digest: DigestSpec;
}

export type VersionState = 'active' | 'quarantined' | 'failed' | 'published';

export type AddOutcome = 'stored' | 'duplicate' | 'noop' | 'published' | 'failed';

export interface VersionInfo {
  fileId: string;
  version: string;
  size: number;
  state: VersionState;
  reason: string | null;
  coveredBytes: number;
  digest: DigestSpec;
  publishedPath: string | null;
}

export interface CheckpointData {
  fileId: string;
  version: string;
  size: number;
  digest: DigestSpec;
  state: VersionState;
  reason: string | null;
  intervals: IntervalTuple[];
  publishedPath: string | null;
}

export type VersionEvent =
  | { type: 'progress'; info: VersionInfo }
  | { type: 'quarantined'; info: VersionInfo }
  | { type: 'failed'; info: VersionInfo }
  | { type: 'published'; info: VersionInfo; path: string };

export interface StoreHooks {
  /** Test hook: fires after data is fsynced but before the checkpoint write. */
  beforeCheckpointWrite?: (key: string) => void;
}

export interface VersionStoreContext {
  dataDir: string;
  checkpointDir: string;
  publishDir: string;
  spillDir: string;
  chunkBytes: number;
  budget: ByteBudget;
  hooks?: StoreHooks;
  emit?: (event: VersionEvent) => void;
}

interface SegmentSource {
  readonly length: number;
  readAt(offset: number, length: number): Promise<Buffer>;
  dispose(): Promise<void>;
}

class BufferSource implements SegmentSource {
  private readonly buf: Buffer;
  constructor(buf: Buffer) {
    this.buf = buf;
  }
  get length(): number {
    return this.buf.length;
  }
  readAt(offset: number, length: number): Promise<Buffer> {
    return Promise.resolve(this.buf.subarray(offset, offset + length));
  }
  dispose(): Promise<void> {
    return Promise.resolve();
  }
}

/** Segment too large for the memory budget: parked on disk, read back in chunks. */
class SpilledFileSource implements SegmentSource {
  readonly length: number;
  private readonly path: string;
  private handle: FileHandle | null = null;

  private constructor(path: string, length: number) {
    this.path = path;
    this.length = length;
  }

  static async spill(buf: Buffer, dir: string, name: string, chunkBytes: number): Promise<SpilledFileSource> {
    const path = join(dir, name);
    const handle = await fs.open(path, 'w');
    try {
      for (let offset = 0; offset < buf.length; offset += chunkBytes) {
        const slice = buf.subarray(offset, Math.min(offset + chunkBytes, buf.length));
        await handle.write(slice);
      }
      await handle.sync();
    } finally {
      await handle.close();
    }
    return new SpilledFileSource(path, buf.length);
  }

  async readAt(offset: number, length: number): Promise<Buffer> {
    if (this.handle === null) this.handle = await fs.open(this.path, 'r');
    const buf = Buffer.allocUnsafe(length);
    const { bytesRead } = await this.handle.read(buf, 0, length, offset);
    if (bytesRead !== length) throw new Error(`short read from spill file at ${offset}`);
    return buf;
  }

  async dispose(): Promise<void> {
    if (this.handle !== null) {
      await this.handle.close();
      this.handle = null;
    }
    await fs.rm(this.path, { force: true });
  }
}

let spillCounter = 0;

async function ensureFileExists(path: string): Promise<void> {
  const handle = await fs.open(path, 'a');
  await handle.close();
}

async function digestFile(path: string, algorithm: string, chunkBytes: number): Promise<string> {
  const hash = createHash(algorithm);
  const handle = await fs.open(path, 'r');
  try {
    let position = 0;
    for (;;) {
      const buf = Buffer.allocUnsafe(chunkBytes);
      const { bytesRead } = await handle.read(buf, 0, chunkBytes, position);
      if (bytesRead === 0) break;
      hash.update(buf.subarray(0, bytesRead));
      position += bytesRead;
    }
  } finally {
    await handle.close();
  }
  return hash.digest('hex');
}

async function moveFile(src: string, dest: string): Promise<void> {
  try {
    await fs.rename(src, dest);
  } catch (err) {
    if ((err as { code?: string }).code === 'EXDEV') {
      await fs.copyFile(src, dest);
      const handle = await fs.open(dest, 'r');
      try {
        await handle.sync();
      } finally {
        await handle.close();
      }
      await fs.rm(src);
    } else {
      throw err;
    }
  }
}

/**
 * Reassembly state for one (fileId, version). Segments are processed serially.
 * Durability order for every accepted segment:
 *   1. verify overlapping bytes against already-trusted on-disk bytes
 *   2. write only the not-yet-covered byte ranges
 *   3. fsync the data file
 *   4. only then record the new coverage in the checkpoint (atomic rename)
 * A crash anywhere leaves the checkpoint describing a subset of what is on
 * disk, so recovery never trusts bytes it cannot account for.
 */
export class VersionStore {
  readonly spec: FileVersionSpec;
  readonly key: string;
  private state: VersionState = 'active';
  private reason: string | null = null;
  private publishedPath: string | null = null;
  private intervals = new IntervalSet();
  private readonly dataPath: string;
  private readonly checkpointPath: string;
  private readonly ctx: VersionStoreContext;
  private tail: Promise<void> = Promise.resolve();

  private constructor(spec: FileVersionSpec, key: string, ctx: VersionStoreContext) {
    this.spec = spec;
    this.key = key;
    this.ctx = ctx;
    this.dataPath = join(ctx.dataDir, `${key}.part`);
    this.checkpointPath = join(ctx.checkpointDir, `${key}.json`);
  }

  static keyFor(fileId: string, version: string): string {
    return `${safeName(fileId)}__${safeName(version)}`;
  }

  static async create(spec: FileVersionSpec, ctx: VersionStoreContext): Promise<VersionStore> {
    const store = new VersionStore(spec, VersionStore.keyFor(spec.fileId, spec.version), ctx);
    await store.persistCheckpoint();
    if (spec.size === 0) await store.finalize();
    return store;
  }

  static async recover(ctx: VersionStoreContext, checkpointPath: string): Promise<VersionStore> {
    const raw = await fs.readFile(checkpointPath, 'utf8');
    const cp = JSON.parse(raw) as CheckpointData;
    const key = basename(checkpointPath, '.json');
    const store = new VersionStore(
      { fileId: cp.fileId, version: cp.version, size: cp.size, digest: cp.digest },
      key,
      ctx,
    );
    store.state = cp.state;
    store.reason = cp.reason;
    store.publishedPath = cp.publishedPath ?? null;
    store.intervals = IntervalSet.from(cp.intervals);

    if (store.state === 'active') {
      const dataExists = await pathExists(store.dataPath);
      if (store.intervals.isComplete(store.spec.size)) {
        if (dataExists) {
          // Crashed between the last checkpoint and finalize: redo it.
          await store.finalize();
        } else if (await store.verifyPublished()) {
          // Crashed after publishing but before recording it.
          store.state = 'published';
          await store.persistCheckpoint();
        } else {
          // Data is gone and nothing trustworthy remains: start over.
          store.intervals = new IntervalSet();
          await store.persistCheckpoint();
        }
      } else if (!dataExists) {
        store.intervals = new IntervalSet();
        await store.persistCheckpoint();
      }
    }
    return store;
  }

  info(): VersionInfo {
    return {
      fileId: this.spec.fileId,
      version: this.spec.version,
      size: this.spec.size,
      state: this.state,
      reason: this.reason,
      coveredBytes: this.intervals.coveredBytes(),
      digest: this.spec.digest,
      publishedPath: this.publishedPath,
    };
  }

  idle(): Promise<void> {
    return this.tail;
  }

  async addSegment(offset: number, data: Uint8Array): Promise<AddOutcome> {
    if (this.state !== 'active') {
      throw new VersionNotWritableError(this.spec.fileId, this.spec.version, this.state);
    }
    const length = data.length;
    if (!Number.isInteger(offset) || offset < 0 || offset + length > this.spec.size) {
      throw new SegmentOutOfBoundsError(this.spec.fileId, this.spec.version, offset, length, this.spec.size);
    }
    if (length === 0) return 'noop';

    const buf = Buffer.isBuffer(data)
      ? data
      : Buffer.from(data.buffer, data.byteOffset, data.byteLength);

    const budget = this.ctx.budget;
    let source: SegmentSource;
    let acquired = 0;
    if (budget.mustSpill(buf.length)) {
      const name = `spill-${process.pid}-${spillCounter++}.bin`;
      source = await SpilledFileSource.spill(buf, this.ctx.spillDir, name, this.ctx.chunkBytes);
    } else {
      await budget.acquire(buf.length);
      acquired = buf.length;
      source = new BufferSource(buf);
    }

    try {
      return await this.enqueue(offset, source);
    } finally {
      if (acquired > 0) budget.release(acquired);
      await source.dispose();
    }
  }

  private enqueue(offset: number, source: SegmentSource): Promise<AddOutcome> {
    const run = this.tail.then(() => this.processSegment(offset, source));
    this.tail = run.then(
      () => undefined,
      () => undefined,
    );
    return run;
  }

  private async processSegment(offset: number, source: SegmentSource): Promise<AddOutcome> {
    if (this.state !== 'active') {
      throw new VersionNotWritableError(this.spec.fileId, this.spec.version, this.state);
    }
    const end = offset + source.length;
    const chunkBytes = this.ctx.chunkBytes;

    await ensureFileExists(this.dataPath);
    const handle = await fs.open(this.dataPath, 'r+');
    let wrote = false;
    try {
      // Phase 1: every overlapping byte must match what is already trusted.
      for (const [rangeStart, rangeEnd] of this.intervals.intersections(offset, end)) {
        let position = rangeStart;
        while (position < rangeEnd) {
          const length = Math.min(chunkBytes, rangeEnd - position);
          const onDisk = Buffer.allocUnsafe(length);
          const { bytesRead } = await handle.read(onDisk, 0, length, position);
          if (bytesRead !== length) throw new Error(`short read from data file at ${position}`);
          const incoming = await source.readAt(position - offset, length);
          if (!onDisk.equals(incoming)) {
            let mismatch = position;
            for (let i = 0; i < length; i++) {
              if (onDisk[i] !== incoming[i]) {
                mismatch = position + i;
                break;
              }
            }
            await this.quarantine(`overlap conflict at byte ${mismatch}`);
            throw new SegmentConflictError(this.spec.fileId, this.spec.version, mismatch);
          }
          position += length;
        }
      }

      // Phase 2: persist only the byte ranges that are still missing.
      for (const [rangeStart, rangeEnd] of this.intervals.complement(offset, end)) {
        let position = rangeStart;
        while (position < rangeEnd) {
          const length = Math.min(chunkBytes, rangeEnd - position);
          const chunk = await source.readAt(position - offset, length);
          await handle.write(chunk, 0, length, position);
          wrote = true;
          position += length;
        }
      }

      // Phase 3: fsync BEFORE the checkpoint may mention these bytes.
      if (wrote) await handle.sync();
    } finally {
      await handle.close();
    }

    if (!wrote) return 'duplicate';

    this.intervals.add(offset, end);
    this.ctx.hooks?.beforeCheckpointWrite?.(this.key);
    await this.persistCheckpoint();
    this.ctx.emit?.({ type: 'progress', info: this.info() });

    if (this.intervals.isComplete(this.spec.size)) {
      await this.finalize();
      return this.state === 'published' ? 'published' : 'failed';
    }
    return 'stored';
  }

  private async finalize(): Promise<void> {
    await ensureFileExists(this.dataPath);
    const hex = await digestFile(this.dataPath, this.spec.digest.algorithm, this.ctx.chunkBytes);
    if (hex.toLowerCase() !== this.spec.digest.hex.toLowerCase()) {
      this.state = 'failed';
      this.reason = `digest mismatch: expected ${this.spec.digest.hex}, got ${hex}`;
      await this.persistCheckpoint();
      this.ctx.emit?.({ type: 'failed', info: this.info() });
      return;
    }
    await fs.mkdir(this.ctx.publishDir, { recursive: true });
    const target = join(this.ctx.publishDir, safeName(this.spec.fileId));
    await moveFile(this.dataPath, target);
    await fsyncDirectory(this.ctx.publishDir);
    this.state = 'published';
    this.publishedPath = target;
    await this.persistCheckpoint();
    this.ctx.emit?.({ type: 'published', info: this.info(), path: target });
  }

  private async quarantine(reason: string): Promise<void> {
    this.state = 'quarantined';
    this.reason = reason;
    await this.persistCheckpoint();
    this.ctx.emit?.({ type: 'quarantined', info: this.info() });
  }

  private async verifyPublished(): Promise<boolean> {
    if (this.publishedPath === null) return false;
    if (!(await pathExists(this.publishedPath))) return false;
    const hex = await digestFile(this.publishedPath, this.spec.digest.algorithm, this.ctx.chunkBytes);
    return hex.toLowerCase() === this.spec.digest.hex.toLowerCase();
  }

  private async persistCheckpoint(): Promise<void> {
    const checkpoint: CheckpointData = {
      fileId: this.spec.fileId,
      version: this.spec.version,
      size: this.spec.size,
      digest: this.spec.digest,
      state: this.state,
      reason: this.reason,
      intervals: this.intervals.toArray(),
      publishedPath: this.publishedPath,
    };
    await writeFileAtomic(this.checkpointPath, JSON.stringify(checkpoint, null, 2));
  }
}
