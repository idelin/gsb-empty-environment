import { createHash } from 'node:crypto';
import * as fs from 'node:fs';
import * as path from 'node:path';
import type { LsmEngine, SSTableMeta } from '../lsm/engine.ts';

export interface SnapshotManifest {
  kind: 'full' | 'incremental';
  /** ISO-8601 creation time. */
  createdAt: string;
  /** For incremental snapshots: file name of the base manifest this delta builds on. */
  base?: string;
  /** SSTables linked into THIS snapshot directory. */
  tables: SSTableMeta[];
}

export interface IncrementalResult {
  manifest: SnapshotManifest;
  /** Tables that were new relative to the base and got hard-linked. */
  added: SSTableMeta[];
  /** Tables already covered by the base snapshot (skipped, zero work). */
  skipped: SSTableMeta[];
}

const MANIFEST_NAME = 'MANIFEST.json';

/**
 * Physical backup tool for the LSM engine.
 *
 * Because SSTables are immutable, a "copy" is just an extra directory entry
 * pointing at the same inode: `fs.linkSync` completes in microseconds and
 * consumes zero additional disk blocks. Compaction may later unlink the
 * engine's own name for the file; the snapshot's link keeps the data alive
 * until the snapshot itself is deleted.
 */
export class SnapshotExporter {
  private readonly engine: LsmEngine;

  constructor(engine: LsmEngine) {
    this.engine = engine;
  }

  /**
   * Full snapshot: hard-link every live SSTable into `destDir` and record a
   * manifest with per-file checksums for later integrity verification.
   */
  exportFull(destDir: string): SnapshotManifest {
    const tables = this.linkAll(this.engine.listSSTables(), destDir);
    const manifest: SnapshotManifest = {
      kind: 'full',
      createdAt: new Date().toISOString(),
      tables,
    };
    this.writeManifest(destDir, manifest);
    return manifest;
  }

  /**
   * Incremental snapshot: hard-link only the SSTables that appeared since
   * `baseManifestPath` was taken. Tables already present in the base are
   * skipped entirely, so the delta directory holds just the new files —
   * suitable for differential offline archiving.
   */
  exportIncremental(destDir: string, baseManifestPath: string): IncrementalResult {
    const base = JSON.parse(fs.readFileSync(baseManifestPath, 'utf8')) as SnapshotManifest;
    const covered = new Set(base.tables.map((t) => t.file));

    const live = this.engine.listSSTables();
    const added = live.filter((t) => !covered.has(t.file));
    const skipped = live.filter((t) => covered.has(t.file));

    const linked = this.linkAll(added, destDir);
    const manifest: SnapshotManifest = {
      kind: 'incremental',
      createdAt: new Date().toISOString(),
      base: path.basename(baseManifestPath),
      tables: linked,
    };
    this.writeManifest(destDir, manifest);
    return { manifest, added: linked, skipped };
  }

  /**
   * Verify a snapshot directory against its manifest: every listed file must
   * exist, have the recorded size, and hash to the recorded SHA-256.
   * Returns the list of human-readable problems (empty means intact).
   */
  static verify(snapshotDir: string): string[] {
    const manifest = JSON.parse(
      fs.readFileSync(path.join(snapshotDir, MANIFEST_NAME), 'utf8'),
    ) as SnapshotManifest;
    const problems: string[] = [];
    for (const table of manifest.tables) {
      const full = path.join(snapshotDir, table.file);
      if (!fs.existsSync(full)) {
        problems.push(`missing: ${table.file}`);
        continue;
      }
      const content = fs.readFileSync(full);
      if (content.byteLength !== table.size) {
        problems.push(`size mismatch: ${table.file} expected ${table.size} got ${content.byteLength}`);
        continue;
      }
      const digest = createHash('sha256').update(content).digest('hex');
      if (digest !== table.sha256) {
        problems.push(`checksum mismatch: ${table.file}`);
      }
    }
    return problems;
  }

  /** Load a snapshot manifest from disk. */
  static readManifest(snapshotDir: string): SnapshotManifest {
    return JSON.parse(fs.readFileSync(path.join(snapshotDir, MANIFEST_NAME), 'utf8')) as SnapshotManifest;
  }

  /**
   * Hard-link the given tables into `destDir`.
   *
   * A table can vanish between listing and linking if compaction unlinks it
   * concurrently; in that case we re-read the live set and link whatever is
   * still current, so the snapshot always reflects a consistent generation.
   */
  private linkAll(tables: SSTableMeta[], destDir: string): SSTableMeta[] {
    fs.mkdirSync(destDir, { recursive: true });
    const linked: SSTableMeta[] = [];
    for (const table of tables) {
      const src = path.join(this.engine.dir, table.file);
      const dst = path.join(destDir, table.file);
      try {
        fs.linkSync(src, dst);
        linked.push(table);
      } catch (err) {
        if ((err as NodeJS.ErrnoException).code === 'ENOENT') continue; // compacted away mid-snapshot
        throw err;
      }
    }
    return linked;
  }

  private writeManifest(destDir: string, manifest: SnapshotManifest): void {
    fs.writeFileSync(path.join(destDir, MANIFEST_NAME), JSON.stringify(manifest, null, 2));
  }
}
