import {
  copyFileSync,
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  renameSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { join } from 'node:path';
import type { LSN, PageId } from './types.ts';
import { pageFileName } from './storage.ts';
import type { BufferPool, DiskStorage, Page } from './storage.ts';
import type { WriteAheadLog } from './wal.ts';

export interface SnapshotPageRef {
  lsn: LSN;
  /** Path of the page image, relative to the snapshot root. */
  file: string;
}

export interface SnapshotManifest {
  id: number;
  createdAt: string;
  /** WAL LSN at snapshot start: the consistency point of the snapshot. */
  baseLSN: LSN;
  parent: number | null;
  /** Full page map; unchanged pages reuse the file of an older snapshot. */
  pages: Record<string, SnapshotPageRef>;
}

export interface ArchiveBundle {
  id: number;
  archivedAt: string;
  baseLSN: LSN;
  /** Self-contained page images, independent of any snapshot directory. */
  pages: Record<string, Page>;
}

/** Minimal FIFO mutex for serializing snapshot lifecycle operations. */
class Mutex {
  private tail: Promise<void> = Promise.resolve();

  acquire(): Promise<() => void> {
    let release!: () => void;
    const next = new Promise<void>((resolve) => {
      release = resolve;
    });
    const prev = this.tail;
    this.tail = prev.then(() => next);
    return prev.then(() => release);
  }
}

function writeJsonAtomic(filePath: string, value: unknown): void {
  const tmp = filePath + '.tmp';
  writeFileSync(tmp, JSON.stringify(value));
  renameSync(tmp, filePath);
}

/**
 * Incremental snapshot & checkpoint archive manager.
 *
 * Low-pause design:
 *  - `beginSnapshot` only records the WAL LSN and installs a copy-on-write
 *    hook on the buffer pool — no data is copied up front.
 *  - Pages modified while a snapshot is open are staged (pre-modification
 *    image) on first write, so the snapshot reflects the exact state at
 *    `baseLSN` without blocking writers.
 *  - `completeSnapshot` writes only pages whose pageLSN changed since the
 *    parent snapshot; unchanged pages are referenced, not copied.
 *  - `archiveSnapshot` converts a snapshot into a self-contained bundle and
 *    re-homes shared page files so dependent snapshots stay restorable.
 */
export class SnapshotManager {
  private readonly storage: DiskStorage;
  private readonly pool: BufferPool;
  private readonly wal: WriteAheadLog;
  readonly snapshotRoot: string;
  readonly archiveRoot: string;
  private readonly mutex = new Mutex();
  private active: {
    id: number;
    baseLSN: LSN;
    parent: SnapshotManifest | null;
    staged: Map<PageId, Page>;
    release: () => void;
    unregister: () => void;
  } | null = null;

  constructor(
    storage: DiskStorage,
    pool: BufferPool,
    wal: WriteAheadLog,
    snapshotRoot: string,
    archiveRoot: string,
  ) {
    this.storage = storage;
    this.pool = pool;
    this.wal = wal;
    this.snapshotRoot = snapshotRoot;
    this.archiveRoot = archiveRoot;
    mkdirSync(snapshotRoot, { recursive: true });
    mkdirSync(archiveRoot, { recursive: true });
  }

  get hasActiveSnapshot(): boolean {
    return this.active !== null;
  }

  get stagedCount(): number {
    return this.active?.staged.size ?? 0;
  }

  private dirFor(id: number): string {
    return join(this.snapshotRoot, `snapshot-${id}`);
  }

  private manifestPath(id: number): string {
    return join(this.dirFor(id), 'manifest.json');
  }

  private archivePath(id: number): string {
    return join(this.archiveRoot, `snapshot-${id}.json`);
  }

  listSnapshots(): number[] {
    if (!existsSync(this.snapshotRoot)) return [];
    return readdirSync(this.snapshotRoot)
      .filter((name) => /^snapshot-\d+$/.test(name))
      .map((name) => Number(name.slice('snapshot-'.length)))
      .sort((a, b) => a - b);
  }

  listArchives(): number[] {
    if (!existsSync(this.archiveRoot)) return [];
    return readdirSync(this.archiveRoot)
      .filter((name) => /^snapshot-\d+\.json$/.test(name))
      .map((name) => Number(name.slice('snapshot-'.length, -'.json'.length)))
      .sort((a, b) => a - b);
  }

  readManifest(id: number): SnapshotManifest {
    const path = this.manifestPath(id);
    if (!existsSync(path)) {
      throw new Error(`snapshot ${id} not found (missing or archived)`);
    }
    return JSON.parse(readFileSync(path, 'utf8')) as SnapshotManifest;
  }

  readArchive(id: number): ArchiveBundle {
    const path = this.archivePath(id);
    if (!existsSync(path)) throw new Error(`archive for snapshot ${id} not found`);
    return JSON.parse(readFileSync(path, 'utf8')) as ArchiveBundle;
  }

  latestManifest(): SnapshotManifest | null {
    const ids = this.listSnapshots();
    if (ids.length === 0) return null;
    return this.readManifest(ids[ids.length - 1]);
  }

  private nextSnapshotId(): number {
    const ids = [...this.listSnapshots(), ...this.listArchives()];
    return ids.length === 0 ? 1 : Math.max(...ids) + 1;
  }

  /**
   * Open a snapshot. Waits for any in-flight snapshot/archive operation, then
   * holds the lifecycle lock until `completeSnapshot` (or `abortSnapshot`).
   */
  async beginSnapshot(): Promise<number> {
    const release = await this.mutex.acquire();
    try {
      if (this.active) throw new Error('a snapshot is already in progress');
      const parent = this.latestManifest();
      const id = this.nextSnapshotId();
      const baseLSN = this.wal.currentLSN;
      const staged = new Map<PageId, Page>();
      const unregister = this.pool.registerBeforeModifyHook((pageId, current) => {
        if (current !== null && !staged.has(pageId)) {
          staged.set(pageId, { ...current });
        }
      });
      this.active = { id, baseLSN, parent, staged, release, unregister };
      return id;
    } catch (err) {
      release();
      throw err;
    }
  }

  /** Materialize the incremental snapshot and release the lifecycle lock. */
  async completeSnapshot(): Promise<SnapshotManifest> {
    const active = this.active;
    if (!active) throw new Error('no snapshot in progress');
    try {
      mkdirSync(this.dirFor(active.id), { recursive: true });
      const pageIds = new Set<PageId>([
        ...this.pool.cachedPageIds(),
        ...this.storage.listPageIds(),
      ]);
      const pages: Record<string, SnapshotPageRef> = {};
      for (const pageId of [...pageIds].sort((a, b) => a - b)) {
        const staged = active.staged.get(pageId);
        const current = staged ?? this.pool.peek(pageId);
        if (!current) continue;
        // Pages created after the consistency point are not part of it.
        if (current.pageLSN > active.baseLSN) continue;
        const prev = active.parent?.pages[String(pageId)];
        if (
          prev &&
          prev.lsn === current.pageLSN &&
          existsSync(join(this.snapshotRoot, prev.file))
        ) {
          pages[String(pageId)] = prev; // incremental: reference, do not copy
        } else {
          const file = `snapshot-${active.id}/${pageFileName(pageId)}`;
          writeFileSync(join(this.snapshotRoot, file), JSON.stringify(current));
          pages[String(pageId)] = { lsn: current.pageLSN, file };
        }
      }
      const manifest: SnapshotManifest = {
        id: active.id,
        createdAt: new Date().toISOString(),
        baseLSN: active.baseLSN,
        parent: active.parent?.id ?? null,
        pages,
      };
      writeJsonAtomic(this.manifestPath(active.id), manifest);
      return manifest;
    } finally {
      active.unregister();
      this.active = null;
      active.release();
    }
  }

  /** Abandon an open snapshot without writing anything. */
  async abortSnapshot(): Promise<void> {
    const active = this.active;
    if (!active) return;
    active.unregister();
    this.active = null;
    active.release();
  }

  /**
   * Restore a snapshot (or its archive) into a target storage directory.
   * Returns the number of pages restored.
   */
  restore(snapshotId: number, target: DiskStorage): number {
    if (existsSync(this.manifestPath(snapshotId))) {
      const manifest = this.readManifest(snapshotId);
      let count = 0;
      for (const [pageId, ref] of Object.entries(manifest.pages)) {
        const page = JSON.parse(
          readFileSync(join(this.snapshotRoot, ref.file), 'utf8'),
        ) as Page;
        target.writePage({ ...page, pageId: Number(pageId) });
        count += 1;
      }
      return count;
    }
    const bundle = this.readArchive(snapshotId);
    let count = 0;
    for (const [pageId, page] of Object.entries(bundle.pages)) {
      target.writePage({ ...page, pageId: Number(pageId) });
      count += 1;
    }
    return count;
  }

  /**
   * Convert a snapshot into a self-contained archive bundle, re-home page
   * files shared with other snapshots, then remove the snapshot directory.
   */
  async archiveSnapshot(id: number): Promise<void> {
    const release = await this.mutex.acquire();
    try {
      const manifest = this.readManifest(id);
      const pages: Record<string, Page> = {};
      for (const [pageId, ref] of Object.entries(manifest.pages)) {
        pages[pageId] = JSON.parse(
          readFileSync(join(this.snapshotRoot, ref.file), 'utf8'),
        ) as Page;
      }
      const bundle: ArchiveBundle = {
        id,
        archivedAt: new Date().toISOString(),
        baseLSN: manifest.baseLSN,
        pages,
      };
      writeJsonAtomic(this.archivePath(id), bundle);

      const prefix = `snapshot-${id}/`;
      for (const otherId of this.listSnapshots()) {
        if (otherId === id) continue;
        const manifestPath = this.manifestPath(otherId);
        const other = JSON.parse(readFileSync(manifestPath, 'utf8')) as SnapshotManifest;
        let changed = false;
        for (const ref of Object.values(other.pages)) {
          if (ref.file.startsWith(prefix)) {
            const dest = `snapshot-${otherId}/${ref.file.slice(prefix.length)}`;
            copyFileSync(join(this.snapshotRoot, ref.file), join(this.snapshotRoot, dest));
            ref.file = dest;
            changed = true;
          }
        }
        if (other.parent === id) {
          other.parent = manifest.parent;
          changed = true;
        }
        if (changed) writeJsonAtomic(manifestPath, other);
      }
      rmSync(this.dirFor(id), { recursive: true, force: true });
    } finally {
      release();
    }
  }

  /** Archive all but the most recent `keepLast` snapshots. */
  async prune(keepLast: number): Promise<number[]> {
    const ids = this.listSnapshots();
    const toArchive = ids.slice(0, Math.max(0, ids.length - keepLast));
    for (const id of toArchive) await this.archiveSnapshot(id);
    return toArchive;
  }

  /** Diagnostics used by leak assertions. */
  debugState(): { active: boolean; stagedCount: number; tmpFiles: string[] } {
    const tmpFiles: string[] = [];
    for (const root of [this.snapshotRoot, this.archiveRoot]) {
      if (!existsSync(root)) continue;
      const walk = (dir: string): void => {
        for (const entry of readdirSync(dir, { withFileTypes: true })) {
          const full = join(dir, entry.name);
          if (entry.isDirectory()) walk(full);
          else if (entry.name.endsWith('.tmp')) tmpFiles.push(full);
        }
      };
      walk(root);
    }
    return {
      active: this.hasActiveSnapshot,
      stagedCount: this.stagedCount,
      tmpFiles,
    };
  }
}
