import { existsSync, linkSync, mkdirSync, readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import type { LSMEngine, SSTableMeta } from '../lsm/engine.ts';
import { sha256 } from '../lsm/sstable.ts';

export interface SnapshotFileEntry {
  name: string;
  size: number;
  ino: number;
  sha256?: string;
}

export interface SnapshotManifest {
  kind: 'full';
  id: string;
  createdAt: string;
  sequence: number;
  files: SnapshotFileEntry[];
}

export interface IncrementalManifest {
  kind: 'incremental';
  id: string;
  base: string;
  createdAt: string;
  baseSequence: number;
  sequence: number;
  added: SnapshotFileEntry[];
  removed: string[];
}

export interface VerifyReport {
  ok: boolean;
  dir: string;
  checked: number;
  errors: string[];
}

export interface SnapshotOptions {
  label?: string;
  // Record per-file sha256 in the manifest so verifySnapshot() can prove
  // content integrity later. Off by default to keep snapshots pure
  // metadata + hard links (zero data IO).
  checksum?: boolean;
}

// Creates zero-copy snapshots by hard-linking immutable SSTables into a
// snapshot directory, and exports incremental archives by diffing the live
// table set against a recorded baseline manifest.
export class SnapshotManager {
  private readonly engine: LSMEngine;
  readonly backupRoot: string;
  private counter = 0;

  constructor(engine: LSMEngine, backupRoot: string) {
    this.engine = engine;
    this.backupRoot = backupRoot;
    mkdirSync(backupRoot, { recursive: true });
  }

  // Full snapshot: hard-link every live SSTable. No data bytes are copied,
  // so cost is O(#files) metadata operations — typically milliseconds.
  createSnapshot(opts: SnapshotOptions = {}): SnapshotManifest {
    const tables = this.engine.listSSTables();
    const id = this.nextId('snap', opts.label);
    const dir = join(this.backupRoot, id);
    mkdirSync(dir, { recursive: true });
    const files = tables.map((t) => this.linkOne(t, dir, opts.checksum ?? false));
    const manifest: SnapshotManifest = {
      kind: 'full',
      id,
      createdAt: new Date().toISOString(),
      sequence: this.engine.sequence,
      files,
    };
    writeFileSync(join(dir, 'MANIFEST.json'), JSON.stringify(manifest, null, 2));
    return manifest;
  }

  // Incremental export: diff the CURRENT live table set against a baseline
  // snapshot, hard-link only the newly appeared SSTables, and record the
  // delta (added/removed) for differential offline archiving.
  exportIncremental(baseId: string, opts: SnapshotOptions = {}): IncrementalManifest {
    const base = this.loadManifest(baseId);
    const live = this.engine.listSSTables();
    const baseNames = new Set(base.files.map((f) => f.name));
    const liveNames = new Set(live.map((t) => t.name));
    const addedTables = live.filter((t) => !baseNames.has(t.name));
    const removed = base.files.filter((f) => !liveNames.has(f.name)).map((f) => f.name);

    const id = this.nextId('incr', `from-${baseId}`);
    const dir = join(this.backupRoot, id);
    mkdirSync(dir, { recursive: true });
    const added = addedTables.map((t) => this.linkOne(t, dir, opts.checksum ?? false));
    const manifest: IncrementalManifest = {
      kind: 'incremental',
      id,
      base: baseId,
      createdAt: new Date().toISOString(),
      baseSequence: base.sequence,
      sequence: this.engine.sequence,
      added,
      removed,
    };
    writeFileSync(join(dir, 'DELTA.json'), JSON.stringify(manifest, null, 2));
    return manifest;
  }

  // Pure manifest diff between two snapshots (no file operations).
  diffSnapshots(baseId: string, targetId: string): { added: string[]; removed: string[] } {
    const base = this.loadManifest(baseId);
    const target = this.loadManifest(targetId);
    const baseNames = new Set(base.files.map((f) => f.name));
    const targetNames = new Set(target.files.map((f) => f.name));
    return {
      added: target.files.filter((f) => !baseNames.has(f.name)).map((f) => f.name),
      removed: base.files.filter((f) => !targetNames.has(f.name)).map((f) => f.name),
    };
  }

  // Verify a snapshot or incremental export directory against its manifest:
  // every listed file must exist, match the recorded size, and (when
  // recorded) match the recorded sha256.
  verify(dirOrId: string): VerifyReport {
    const dir = this.resolveDir(dirOrId);
    const manifestPath = existsSync(join(dir, 'MANIFEST.json'))
      ? join(dir, 'MANIFEST.json')
      : join(dir, 'DELTA.json');
    const errors: string[] = [];
    if (!existsSync(manifestPath)) {
      return { ok: false, dir, checked: 0, errors: [`no MANIFEST.json or DELTA.json in ${dir}`] };
    }
    const manifest = JSON.parse(readFileSync(manifestPath, 'utf8')) as SnapshotManifest | IncrementalManifest;
    const files: SnapshotFileEntry[] = manifest.kind === 'full' ? manifest.files : manifest.added;
    let checked = 0;
    for (const f of files) {
      const p = join(dir, f.name);
      if (!existsSync(p)) {
        errors.push(`missing file: ${f.name}`);
        continue;
      }
      const st = statSync(p);
      if (st.size !== f.size) {
        errors.push(`size mismatch: ${f.name} expected ${f.size}, got ${st.size}`);
        continue;
      }
      if (f.sha256) {
        const actual = sha256(readFileSync(p));
        if (actual !== f.sha256) {
          errors.push(`sha256 mismatch: ${f.name} expected ${f.sha256}, got ${actual}`);
          continue;
        }
      }
      checked += 1;
    }
    return { ok: errors.length === 0, dir, checked, errors };
  }

  snapshotDir(id: string): string {
    return join(this.backupRoot, id);
  }

  loadManifest(id: string): SnapshotManifest {
    const p = join(this.backupRoot, id, 'MANIFEST.json');
    if (!existsSync(p)) throw new Error(`snapshot not found: ${id}`);
    return JSON.parse(readFileSync(p, 'utf8')) as SnapshotManifest;
  }

  listBackups(): Array<SnapshotManifest | IncrementalManifest> {
    const out: Array<SnapshotManifest | IncrementalManifest> = [];
    for (const name of readdirSync(this.backupRoot).sort()) {
      const dir = join(this.backupRoot, name);
      for (const file of ['MANIFEST.json', 'DELTA.json']) {
        const p = join(dir, file);
        if (existsSync(p)) {
          out.push(JSON.parse(readFileSync(p, 'utf8')));
          break;
        }
      }
    }
    return out;
  }

  private linkOne(t: SSTableMeta, dir: string, withChecksum: boolean): SnapshotFileEntry {
    const dest = join(dir, t.name);
    linkSync(t.path, dest); // hard link: same inode, zero data copy
    const st = statSync(dest);
    const entry: SnapshotFileEntry = { name: t.name, size: st.size, ino: st.ino };
    if (withChecksum) entry.sha256 = sha256(readFileSync(dest));
    return entry;
  }

  private resolveDir(dirOrId: string): string {
    const asId = join(this.backupRoot, dirOrId);
    return existsSync(asId) ? asId : resolve(dirOrId);
  }

  private nextId(prefix: string, label?: string): string {
    this.counter += 1;
    const stamp = new Date().toISOString().replace(/[:.]/g, '-');
    const suffix = label ? `-${label.replace(/[^a-zA-Z0-9_-]/g, '-')}` : '';
    return `${prefix}-${stamp}-${this.counter}${suffix}`;
  }
}
