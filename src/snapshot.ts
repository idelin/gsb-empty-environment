import { existsSync, mkdirSync, readFileSync, writeFileSync, renameSync, copyFileSync } from 'node:fs';
import { join } from 'node:path';
import type { BufferPool, PageImage } from './storage.ts';
import type { LogManager } from './wal.ts';

export interface SnapshotMeta {
  id: number;
  createdAt: string;
  /** LSN horizon of the previous snapshot: this delta covers (baseLSN, horizonLSN]. */
  baseLSN: number;
  horizonLSN: number;
  parentId: number | null;
  pageCount: number;
  file: string;
}

interface Manifest {
  snapshots: SnapshotMeta[];
}

interface DeltaFile {
  meta: SnapshotMeta;
  pages: Record<string, PageImage>;
}

export interface SnapshotOptions {
  /** Yield to the event loop every N captured pages to keep pause times low. */
  yieldEvery?: number;
  /** Test hook invoked after each batch, allowing interleaved concurrent writes. */
  onBatch?: (capturedSoFar: number) => void | Promise<void>;
}

const tick = () => new Promise<void>((resolve) => setImmediate(resolve));

/**
 * Incremental snapshot manager. Each snapshot persists only the pages whose
 * pageLSN falls in (previousHorizon, currentHorizon], forming a delta chain
 * that can be archived and restored. Copy-on-write keeps snapshots consistent
 * with the horizon LSN while writes continue concurrently (low pause).
 */
export class IncrementalSnapshotManager {
  private readonly archiveDir: string;
  private manifest: Manifest;
  private pendingCapture = new Map<number, PageImage>();
  private activeHorizon: number | null = null;
  private readonly pool: BufferPool;
  private readonly wal: LogManager;
  private readonly cowHook = (pageId: number, oldImage: PageImage): void => {
    if (this.activeHorizon === null) return;
    if (oldImage.pageLSN > this.activeHorizon) return;
    if (!this.pendingCapture.has(pageId)) this.pendingCapture.set(pageId, oldImage);
  };

  constructor(
    dir: string,
    pool: BufferPool,
    wal: LogManager,
  ) {
    this.pool = pool;
    this.wal = wal;
    this.archiveDir = join(dir, 'archive');
    mkdirSync(this.archiveDir, { recursive: true });
    this.manifest = this.loadManifest();
  }

  get snapshots(): readonly SnapshotMeta[] {
    return this.manifest.snapshots;
  }

  get latest(): SnapshotMeta | null {
    const all = this.manifest.snapshots;
    return all.length === 0 ? null : all[all.length - 1];
  }

  /** Leak probe: non-zero while a snapshot is mid-flight. */
  get pendingCaptureCount(): number {
    return this.pendingCapture.size;
  }

  get isCapturing(): boolean {
    return this.activeHorizon !== null;
  }

  async createSnapshot(options: SnapshotOptions = {}): Promise<SnapshotMeta> {
    if (this.activeHorizon !== null) throw new Error('snapshot already in progress');
    const yieldEvery = options.yieldEvery ?? 256;
    const horizonLSN = this.wal.flushedLSN;
    const baseLSN = this.latest?.horizonLSN ?? 0;
    const parentId = this.latest?.id ?? null;

    this.activeHorizon = horizonLSN;
    this.pool.addCowHook(this.cowHook);
    try {
      const candidates: number[] = [];
      for (const [pageId, image] of this.pool.allPages()) {
        if (image.pageLSN > baseLSN && image.pageLSN <= horizonLSN) candidates.push(pageId);
      }
      const pages: Record<string, PageImage> = {};
      let captured = 0;
      for (const pageId of candidates) {
        const image = this.pendingCapture.get(pageId) ?? this.pool.readPage(pageId);
        if (image.pageLSN > baseLSN && image.pageLSN <= horizonLSN) {
          pages[String(pageId)] = image;
        }
        captured++;
        if (captured % yieldEvery === 0) {
          await options.onBatch?.(captured);
          await tick();
        }
      }
      // Drain COW images for candidates not yet visited when they were written.
      for (const [pageId, image] of this.pendingCapture) {
        if (image.pageLSN > baseLSN && image.pageLSN <= horizonLSN && pages[String(pageId)] === undefined) {
          pages[String(pageId)] = image;
        }
      }

      const meta: SnapshotMeta = {
        id: (this.latest?.id ?? 0) + 1,
        createdAt: new Date().toISOString(),
        baseLSN,
        horizonLSN,
        parentId,
        pageCount: Object.keys(pages).length,
        file: `snap-${(this.latest?.id ?? 0) + 1}.json`,
      };
      const delta: DeltaFile = { meta, pages };
      const tmp = join(this.archiveDir, meta.file + '.tmp');
      writeFileSync(tmp, JSON.stringify(delta), 'utf8');
      renameSync(tmp, join(this.archiveDir, meta.file));
      this.manifest.snapshots.push(meta);
      this.saveManifest();
      return meta;
    } finally {
      this.pool.removeCowHook(this.cowHook);
      this.pendingCapture.clear();
      this.activeHorizon = null;
    }
  }

  /** Restore the full page map as of the given snapshot (default: latest). */
  restore(targetId?: number): Map<number, PageImage> {
    const chain = this.chainTo(targetId ?? this.latest?.id);
    const pages = new Map<number, PageImage>();
    for (const meta of chain) {
      const delta = this.readDelta(meta);
      for (const [pageId, image] of Object.entries(delta.pages)) {
        pages.set(Number(pageId), image);
      }
    }
    return pages;
  }

  /**
   * Archive a checkpoint: copy the WAL prefix up to the checkpoint LSN into
   * the archive directory, take an incremental snapshot, then prune the
   * live WAL. keepFromLSN is the safe prune horizon: the oldest begin LSN of
   * any transaction still active (its undo chain must survive in the live WAL).
   */
  async archiveCheckpoint(checkpointLSN: number, keepFromLSN?: number): Promise<SnapshotMeta> {
    const records = this.wal.scan().filter((r) => r.lsn <= checkpointLSN);
    const segPath = join(this.archiveDir, `wal-upto-${checkpointLSN}.log`);
    writeFileSync(segPath, records.map((r) => JSON.stringify(r)).join('\n') + '\n', 'utf8');
    const meta = await this.createSnapshot();
    this.wal.truncateBefore(keepFromLSN ?? checkpointLSN + 1);
    return meta;
  }

  /** Restore an archived snapshot chain directly into durable storage. */
  restoreIntoStorage(storage: { save(pages: Map<number, PageImage>): void }, targetId?: number): void {
    storage.save(this.restore(targetId));
  }

  private chainTo(targetId: number | undefined): SnapshotMeta[] {
    if (targetId === undefined) throw new Error('no snapshots available');
    const byId = new Map(this.manifest.snapshots.map((s) => [s.id, s]));
    const chain: SnapshotMeta[] = [];
    let cursor = byId.get(targetId);
    if (!cursor) throw new Error(`unknown snapshot ${targetId}`);
    while (cursor) {
      chain.unshift(cursor);
      cursor = cursor.parentId === null ? undefined : byId.get(cursor.parentId);
      if (cursor === undefined && chain[0].parentId !== null) {
        throw new Error(`broken snapshot chain: missing parent of ${chain[0].id}`);
      }
    }
    return chain;
  }

  private readDelta(meta: SnapshotMeta): DeltaFile {
    const path = join(this.archiveDir, meta.file);
    if (!existsSync(path)) throw new Error(`missing delta file for snapshot ${meta.id}`);
    return JSON.parse(readFileSync(path, 'utf8')) as DeltaFile;
  }

  private manifestPath(): string {
    return join(this.archiveDir, 'manifest.json');
  }

  private loadManifest(): Manifest {
    const path = this.manifestPath();
    if (!existsSync(path)) return { snapshots: [] };
    return JSON.parse(readFileSync(path, 'utf8')) as Manifest;
  }

  private saveManifest(): void {
    const tmp = this.manifestPath() + '.tmp';
    writeFileSync(tmp, JSON.stringify(this.manifest, null, 2), 'utf8');
    renameSync(tmp, this.manifestPath());
  }
}

/** Convenience: copy an archived WAL segment back for inspection in tests. */
export function readArchivedSegment(dir: string, checkpointLSN: number): string {
  return readFileSync(join(dir, 'archive', `wal-upto-${checkpointLSN}.log`), 'utf8');
}

export function copyArchivedSegment(dir: string, checkpointLSN: number, dest: string): void {
  copyFileSync(join(dir, 'archive', `wal-upto-${checkpointLSN}.log`), dest);
}
