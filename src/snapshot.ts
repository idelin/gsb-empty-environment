/**
 * Incremental snapshot & checkpoint archive engine.
 *
 * VersionedStore: MVCC-style state store; every mutation bumps a
 * monotonic version and records the key in a dirty set.
 * SnapshotEngine: produces lightweight incremental snapshots containing
 * only the entries dirtied since the previous snapshot (low pause: the
 * dirty set is swapped out in a single synchronous critical section),
 * and folds state into checkpoints archived as self-describing protobuf
 * binaries (encoded with this package's own reflection codec).
 * Concurrency: an async mutex serializes snapshot/checkpoint/restore;
 * the mutation-critical section never awaits, so it is atomic.
 * Leak safety: snapshots/archives are tracked in a registry and must be
 * released; assertNoLeaks() verifies strict accounting.
 */
import { defineMessage } from "./schema.ts";
import type { MessageRecord } from "./schema.ts";
import { ProtobufDecodeError } from "./varint.ts";

export const ARCHIVE_MAGIC = 0x5042534e; // "PBSN"
export const ARCHIVE_FORMAT_VERSION = 1;

const EntrySchema = defineMessage("ArchiveEntry", [
  { name: "key", fieldNumber: 1, type: "string" },
  { name: "value", fieldNumber: 2, type: "bytes" },
  { name: "deleted", fieldNumber: 3, type: "bool" },
]);

const DeltaSchema = defineMessage("ArchiveDelta", [
  { name: "snapshotId", fieldNumber: 1, type: "uint64" },
  { name: "fromVersion", fieldNumber: 2, type: "uint64" },
  { name: "toVersion", fieldNumber: 3, type: "uint64" },
  { name: "entries", fieldNumber: 4, type: "message", repeated: true, schema: EntrySchema },
]);

const HeaderSchema = defineMessage("ArchiveHeader", [
  { name: "magic", fieldNumber: 1, type: "fixed32" },
  { name: "formatVersion", fieldNumber: 2, type: "uint32" },
  { name: "baseVersion", fieldNumber: 3, type: "uint64" },
  { name: "entryCount", fieldNumber: 4, type: "uint32" },
  { name: "deltaCount", fieldNumber: 5, type: "uint32" },
  { name: "deltaFromVersion", fieldNumber: 6, type: "uint64" },
]);

const ArchiveSchema = defineMessage("CheckpointArchive", [
  { name: "header", fieldNumber: 1, type: "message", schema: HeaderSchema },
  { name: "baseEntries", fieldNumber: 2, type: "message", repeated: true, schema: EntrySchema },
  { name: "deltas", fieldNumber: 3, type: "message", repeated: true, schema: DeltaSchema },
]);

export class LeakError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "LeakError";
  }
}

/** Tracks acquire/release pairs and fails loudly on imbalance. */
export class ResourceRegistry {
  private readonly live = new Set<object>();
  private totalAcquired = 0;

  track<T extends object>(resource: T): T {
    this.live.add(resource);
    this.totalAcquired++;
    return resource;
  }

  release(resource: object): void {
    if (!this.live.delete(resource)) {
      throw new LeakError("release of untracked or already-released resource");
    }
  }

  get liveCount(): number {
    return this.live.size;
  }

  get acquired(): number {
    return this.totalAcquired;
  }

  assertNoLeaks(context = "registry"): void {
    if (this.live.size !== 0) {
      throw new LeakError(`${context}: ${this.live.size} resource(s) still live`);
    }
  }
}

export interface DirtyEntry {
  readonly key: string;
  /** null marks a deletion tombstone. */
  readonly value: Uint8Array | null;
}

export class VersionedStore {
  private readonly data = new Map<string, Uint8Array>();
  private readonly dirty = new Map<string, Uint8Array | null>();
  private version = 0n;

  get currentVersion(): bigint {
    return this.version;
  }

  get size(): number {
    return this.data.size;
  }

  get dirtyCount(): number {
    return this.dirty.size;
  }

  /** Synchronous mutation: atomic with respect to snapshot capture. */
  set(key: string, value: Uint8Array): bigint {
    const copy = value.slice(); // detach from caller-owned buffer
    this.data.set(key, copy);
    this.dirty.set(key, copy);
    return ++this.version;
  }

  delete(key: string): bigint {
    this.data.delete(key);
    this.dirty.set(key, null);
    return ++this.version;
  }

  get(key: string): Uint8Array | undefined {
    const v = this.data.get(key);
    return v === undefined ? undefined : v.slice();
  }

  has(key: string): boolean {
    return this.data.has(key);
  }

  keys(): string[] {
    return [...this.data.keys()];
  }

  /**
   * Atomically drain the dirty set. Runs fully synchronously so a
   * concurrent mutation can never observe a half-drained state.
   */
  drainDirty(): { entries: DirtyEntry[]; toVersion: bigint } {
    const entries: DirtyEntry[] = [];
    for (const [key, value] of this.dirty) {
      entries.push({ key, value: value === null ? null : value.slice() });
    }
    this.dirty.clear();
    return { entries, toVersion: this.version };
  }

  /** Deep-copied full state, used by checkpoints. */
  copyState(): Map<string, Uint8Array> {
    const out = new Map<string, Uint8Array>();
    for (const [k, v] of this.data) out.set(k, v.slice());
    return out;
  }

  /** Total payload bytes currently held (for overhead accounting). */
  get byteSize(): number {
    let n = 0;
    for (const v of this.data.values()) n += v.length;
    return n;
  }

  /** Restore support: force the version counter (used after replay). */
  rebaseVersion(version: bigint): void {
    this.version = version;
  }
}

export interface IncrementalSnapshot {
  readonly id: bigint;
  readonly fromVersion: bigint;
  readonly toVersion: bigint;
  readonly entries: readonly DirtyEntry[];
  readonly createdAtMs: number;
}

export class SnapshotHandle {
  private released = false;
  readonly snapshot: IncrementalSnapshot;
  private readonly registry: ResourceRegistry;
  constructor(
    snapshot: IncrementalSnapshot,
    registry: ResourceRegistry,
  ) {
    this.snapshot = snapshot;
    this.registry = registry;
    registry.track(this);
  }
  release(): void {
    if (this.released) {
      throw new LeakError(`snapshot ${this.snapshot.id} released twice`);
    }
    this.released = true;
    this.registry.release(this);
  }
  get isReleased(): boolean {
    return this.released;
  }
}

/** Promise-chain async mutex: no busy waiting, FIFO fairness. */
class AsyncMutex {
  private tail: Promise<unknown> = Promise.resolve();

  async runExclusive<T>(fn: () => T | Promise<T>): Promise<T> {
    const prev = this.tail;
    let release!: () => void;
    this.tail = new Promise<void>((resolve) => (release = resolve));
    await prev;
    try {
      return await fn();
    } finally {
      release();
    }
  }
}

export interface CheckpointArchiveData {
  readonly baseVersion: bigint;
  /** Version the delta chain starts from (== baseVersion when empty). */
  readonly deltaFromVersion: bigint;
  readonly baseEntries: readonly DirtyEntry[];
  readonly deltas: readonly IncrementalSnapshot[];
}

export class CheckpointArchive {
  private released = false;
  readonly data: CheckpointArchiveData;
  readonly bytes: Uint8Array;
  private readonly registry: ResourceRegistry;
  constructor(
    data: CheckpointArchiveData,
    bytes: Uint8Array,
    registry: ResourceRegistry,
  ) {
    this.data = data;
    this.bytes = bytes;
    this.registry = registry;
    registry.track(this);
  }
  release(): void {
    if (this.released) throw new LeakError("archive released twice");
    this.released = true;
    this.registry.release(this);
  }
  get isReleased(): boolean {
    return this.released;
  }
}

export class SnapshotError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SnapshotError";
  }
}

export class SnapshotEngine {
  private readonly mutex = new AsyncMutex();
  private readonly registry = new ResourceRegistry();
  private readonly pending: IncrementalSnapshot[] = [];
  private readonly store: VersionedStore;
  private nextSnapshotId = 1n;
  private lastSnapshotVersion = 0n;
  private lastCheckpointVersion = 0n;
  private disposed = false;

  constructor(store: VersionedStore) {
    this.store = store;
  }

  get registryStats(): { live: number; acquired: number } {
    return { live: this.registry.liveCount, acquired: this.registry.acquired };
  }

  get pendingDeltaCount(): number {
    return this.pending.length;
  }

  private assertActive(): void {
    if (this.disposed) throw new SnapshotError("engine is disposed");
  }

  /**
   * Capture an incremental snapshot: only entries dirtied since the
   * previous snapshot. The store's dirty set is swapped synchronously,
   * so the pause is O(dirty) with no locking of mutations.
   */
  async snapshot(): Promise<SnapshotHandle> {
    this.assertActive();
    return this.mutex.runExclusive(() => {
      const { entries, toVersion } = this.store.drainDirty();
      const snap: IncrementalSnapshot = {
        id: this.nextSnapshotId++,
        fromVersion: this.lastSnapshotVersion,
        toVersion,
        entries,
        createdAtMs: Date.now(),
      };
      this.lastSnapshotVersion = toVersion;
      this.pending.push(snap);
      return new SnapshotHandle(snap, this.registry);
    });
  }

  /**
   * Fold the full current state plus all pending deltas into a
   * self-describing protobuf archive. Pending deltas are retained in the
   * archive so a restore can replay history after the checkpoint base.
   */
  async checkpoint(): Promise<CheckpointArchive> {
    this.assertActive();
    return this.mutex.runExclusive(() => {
      // Flush any un-snapshotted mutations into a final delta first.
      const { entries, toVersion } = this.store.drainDirty();
      if (entries.length > 0 || toVersion !== this.lastSnapshotVersion) {
        this.pending.push({
          id: this.nextSnapshotId++,
          fromVersion: this.lastSnapshotVersion,
          toVersion,
          entries,
          createdAtMs: Date.now(),
        });
        this.lastSnapshotVersion = toVersion;
      }

      const base = this.store.copyState();
      const baseEntries: DirtyEntry[] = [...base.entries()].map(([key, value]) => ({
        key,
        value,
      }));
      const deltas = [...this.pending];
      const data: CheckpointArchiveData = {
        baseVersion: toVersion,
        deltaFromVersion: this.lastCheckpointVersion,
        baseEntries,
        deltas,
      };
      const bytes = encodeArchive(data);
      this.lastCheckpointVersion = toVersion;
      this.pending.length = 0;
      return new CheckpointArchive(data, bytes, this.registry);
    });
  }

  /**
   * Restore an archive into a fresh store. Validates the magic header,
   * format version and the delta chain: deltas must be contiguous, start
   * at the recorded deltaFromVersion and end exactly at baseVersion.
   * When the chain covers the full history (from version 0) it is
   * additionally replayed and must reproduce the base state bit-for-bit.
   */
  static restore(bytes: Uint8Array): { store: VersionedStore; version: bigint } {
    const data = decodeArchive(bytes);
    let expected = data.deltaFromVersion;
    for (const delta of data.deltas) {
      if (delta.fromVersion !== expected) {
        throw new SnapshotError(
          `delta chain gap: snapshot ${delta.id} starts at version ` +
            `${delta.fromVersion}, expected ${expected}`,
        );
      }
      expected = delta.toVersion;
    }
    if (expected !== data.baseVersion) {
      throw new SnapshotError(
        `delta chain ends at version ${expected}, archive base is ${data.baseVersion}`,
      );
    }

    // Primary path: materialize the checkpoint base.
    const store = new VersionedStore();
    for (const e of data.baseEntries) {
      if (e.value !== null) store.set(e.key, e.value);
    }
    store.rebaseVersion(data.baseVersion);

    // Cross-validation: replay the delta chain from empty and require
    // an identical final state, catching any archive inconsistency.
    if (data.deltaFromVersion === 0n) {
      const replay = new VersionedStore();
      for (const delta of data.deltas) {
        for (const e of delta.entries) {
          if (e.value === null) replay.delete(e.key);
          else replay.set(e.key, e.value);
        }
      }
      if (!statesEqual(store, replay)) {
        throw new SnapshotError(
          "archive inconsistency: delta replay does not match checkpoint base",
        );
      }
    }
    return { store, version: data.baseVersion };
  }

  /** Release all tracked resources; throws if anything is still live. */
  dispose(): void {
    this.registry.assertNoLeaks("SnapshotEngine.dispose");
    this.disposed = true;
  }

  assertNoLeaks(): void {
    this.registry.assertNoLeaks("SnapshotEngine");
  }
}

function statesEqual(a: VersionedStore, b: VersionedStore): boolean {
  if (a.size !== b.size) return false;
  for (const key of a.keys()) {
    const va = a.get(key);
    const vb = b.get(key);
    if (va === undefined || vb === undefined) return false;
    if (va.length !== vb.length) return false;
    for (let i = 0; i < va.length; i++) if (va[i] !== vb[i]) return false;
  }
  return true;
}

function entryToRecord(e: DirtyEntry): MessageRecord {
  return {
    key: e.key,
    value: e.value === null ? new Uint8Array(0) : e.value,
    deleted: e.value === null,
  };
}

function entryFromRecord(r: MessageRecord): DirtyEntry {
  const deleted = r.deleted === true;
  const value = r.value instanceof Uint8Array ? r.value : new Uint8Array(0);
  if (deleted) return { key: String(r.key), value: null };
  return { key: String(r.key), value };
}

export function encodeArchive(data: CheckpointArchiveData): Uint8Array {
  const record: MessageRecord = {
    header: {
      magic: ARCHIVE_MAGIC,
      formatVersion: ARCHIVE_FORMAT_VERSION,
      baseVersion: data.baseVersion,
      deltaFromVersion: data.deltaFromVersion,
      entryCount: data.baseEntries.length,
      deltaCount: data.deltas.length,
    },
    baseEntries: data.baseEntries.map(entryToRecord),
    deltas: data.deltas.map((d) => ({
      snapshotId: d.id,
      fromVersion: d.fromVersion,
      toVersion: d.toVersion,
      entries: d.entries.map(entryToRecord),
    })),
  };
  return ArchiveSchema.encode(record);
}

export function decodeArchive(bytes: Uint8Array): CheckpointArchiveData {
  const record = ArchiveSchema.decode(bytes);
  const header = (record.header ?? {}) as MessageRecord;
  if (header.magic !== ARCHIVE_MAGIC) {
    throw new ProtobufDecodeError(
      `bad archive magic: 0x${Number(header.magic ?? 0).toString(16)}`,
    );
  }
  if (header.formatVersion !== ARCHIVE_FORMAT_VERSION) {
    throw new SnapshotError(
      `unsupported archive format version ${header.formatVersion}`,
    );
  }
  const baseEntries = ((record.baseEntries as MessageRecord[]) ?? []).map(entryFromRecord);
  const deltas: IncrementalSnapshot[] = ((record.deltas as MessageRecord[]) ?? []).map(
    (d) => ({
      id: BigInt(d.snapshotId as bigint),
      fromVersion: BigInt(d.fromVersion as bigint),
      toVersion: BigInt(d.toVersion as bigint),
      entries: ((d.entries as MessageRecord[]) ?? []).map(entryFromRecord),
      createdAtMs: 0,
    }),
  );
  if (baseEntries.length !== Number(header.entryCount)) {
    throw new SnapshotError("archive header entry count mismatch");
  }
  if (deltas.length !== Number(header.deltaCount)) {
    throw new SnapshotError("archive header delta count mismatch");
  }
  return {
    baseVersion: BigInt(header.baseVersion as bigint),
    deltaFromVersion: BigInt(header.deltaFromVersion as bigint),
    baseEntries,
    deltas,
  };
}
