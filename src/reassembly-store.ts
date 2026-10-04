import { EventEmitter } from 'node:events';
import { promises as fs } from 'node:fs';
import { join } from 'node:path';
import { ByteBudget } from './budget.ts';
import { SpecConflictError, UnknownVersionError } from './errors.ts';
import { VersionStore } from './version-store.ts';
import type {
  AddOutcome,
  FileVersionSpec,
  StoreHooks,
  VersionEvent,
  VersionInfo,
  VersionStoreContext,
} from './version-store.ts';

export interface ReassemblyStoreOptions {
  /** Working directory for data files, checkpoints, spill and published output. */
  dir: string;
  /**
   * Hard cap on segment bytes the library retains in memory at any moment.
   * Segments larger than this are spilled to disk and processed in chunks.
   */
  maxMemoryBytes?: number;
  /** Where verified files are moved to. Defaults to <dir>/published. */
  publishDir?: string;
  /** I/O chunk size for overlap verification, digest and spill. */
  chunkBytes?: number;
  hooks?: StoreHooks;
}

export interface SegmentInput {
  fileId: string;
  version: string;
  /** Absolute byte offset of data within the file. */
  offset: number;
  /**
   * Segment bytes. Not copied: the caller must not mutate the buffer until
   * the returned promise settles.
   */
  data: Uint8Array;
}

const DEFAULT_MAX_MEMORY_BYTES = 8 * 1024 * 1024;
const DEFAULT_CHUNK_BYTES = 64 * 1024;

function mapKey(fileId: string, version: string): string {
  return `${fileId}\n${version}`;
}

function validateSpec(spec: FileVersionSpec): void {
  if (typeof spec.fileId !== 'string' || spec.fileId.length === 0) {
    throw new TypeError('fileId must be a non-empty string');
  }
  if (typeof spec.version !== 'string' || spec.version.length === 0) {
    throw new TypeError('version must be a non-empty string');
  }
  if (!Number.isInteger(spec.size) || spec.size < 0) {
    throw new TypeError(`size must be a non-negative integer, got ${spec.size}`);
  }
  if (typeof spec.digest?.algorithm !== 'string' || spec.digest.algorithm.length === 0) {
    throw new TypeError('digest.algorithm must be a non-empty string');
  }
  if (typeof spec.digest?.hex !== 'string' || !/^[0-9a-fA-F]+$/.test(spec.digest.hex)) {
    throw new TypeError('digest.hex must be a non-empty hex string');
  }
}

export class ReassemblyStore extends EventEmitter {
  private readonly versions = new Map<string, VersionStore>();
  private readonly budget: ByteBudget;
  private readonly ctx: VersionStoreContext;
  private readonly checkpointDir: string;

  private constructor(options: ReassemblyStoreOptions) {
    super();
    this.budget = new ByteBudget(options.maxMemoryBytes ?? DEFAULT_MAX_MEMORY_BYTES);
    this.checkpointDir = join(options.dir, 'checkpoints');
    this.ctx = {
      dataDir: join(options.dir, 'data'),
      checkpointDir: this.checkpointDir,
      publishDir: options.publishDir ?? join(options.dir, 'published'),
      spillDir: join(options.dir, 'spill'),
      chunkBytes: options.chunkBytes ?? DEFAULT_CHUNK_BYTES,
      budget: this.budget,
      hooks: options.hooks,
      emit: (event: VersionEvent) => this.emit(event.type, event),
    };
  }

  static async open(options: ReassemblyStoreOptions): Promise<ReassemblyStore> {
    const store = new ReassemblyStore(options);
    await store.init();
    return store;
  }

  private async init(): Promise<void> {
    await fs.mkdir(this.ctx.dataDir, { recursive: true });
    await fs.mkdir(this.checkpointDir, { recursive: true });
    await fs.mkdir(this.ctx.publishDir, { recursive: true });
    // Spill files are never part of the durable state: wipe leftovers from a crash.
    await fs.rm(this.ctx.spillDir, { recursive: true, force: true });
    await fs.mkdir(this.ctx.spillDir, { recursive: true });

    const entries = await fs.readdir(this.checkpointDir);
    for (const entry of entries) {
      if (!entry.endsWith('.json')) continue; // ignores *.json.tmp crash leftovers
      const store = await VersionStore.recover(this.ctx, join(this.checkpointDir, entry));
      this.versions.set(mapKey(store.spec.fileId, store.spec.version), store);
    }
  }

  /**
   * Declare the expected size and digest of a file version. Idempotent for an
   * identical spec; throws SpecConflictError if a different spec is registered
   * for the same (fileId, version).
   */
  async registerFile(spec: FileVersionSpec): Promise<VersionInfo> {
    validateSpec(spec);
    const key = mapKey(spec.fileId, spec.version);
    const existing = this.versions.get(key);
    if (existing !== undefined) {
      const current = existing.spec;
      const sameDigest =
        current.digest.algorithm === spec.digest.algorithm &&
        current.digest.hex.toLowerCase() === spec.digest.hex.toLowerCase();
      if (current.size !== spec.size || !sameDigest) {
        throw new SpecConflictError(spec.fileId, spec.version);
      }
      return existing.info();
    }
    const store = await VersionStore.create(spec, this.ctx);
    this.versions.set(key, store);
    return store.info();
  }

  async addSegment(segment: SegmentInput): Promise<AddOutcome> {
    const store = this.versions.get(mapKey(segment.fileId, segment.version));
    if (store === undefined) throw new UnknownVersionError(segment.fileId, segment.version);
    return store.addSegment(segment.offset, segment.data);
  }

  getVersion(fileId: string, version: string): VersionInfo | undefined {
    return this.versions.get(mapKey(fileId, version))?.info();
  }

  listVersions(): VersionInfo[] {
    return [...this.versions.values()].map((store) => store.info());
  }

  /** Peak segment bytes held in memory since open(); never exceeds maxMemoryBytes. */
  get memoryHighWaterBytes(): number {
    return this.budget.highWaterBytes;
  }

  get memoryInFlightBytes(): number {
    return this.budget.inFlightBytes;
  }

  /** Wait for all queued segment processing to settle. */
  async close(): Promise<void> {
    await Promise.all([...this.versions.values()].map((store) => store.idle()));
  }
}
