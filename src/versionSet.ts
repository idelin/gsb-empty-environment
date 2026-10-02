// VersionSet: crash-consistent manager of the LSM tree's file metadata.
//
// Invariants:
//  - The MANIFEST is the single source of truth; in-memory state is a
//    replayed projection of it.
//  - A VersionEdit becomes visible only after it is appended + fsynced
//    (logAndApply). SSTable files written before that point but never
//    committed are orphans and are deleted by removeObsoleteFiles().
//  - CURRENT points at the live MANIFEST file.

import * as fs from 'node:fs';
import * as path from 'node:path';
import { ManifestWriter, replayManifest, truncateToValid } from './manifest.ts';
import { emptyEdit, type FileMetaData, type VersionEdit } from './versionEdit.ts';

export const NUM_LEVELS = 6;

export function sstableFileName(n: number): string {
  return `${String(n).padStart(6, '0')}.sst`;
}

export function manifestFileName(n: number): string {
  return `MANIFEST-${String(n).padStart(6, '0')}`;
}

export class VersionSet {
  readonly dir: string;
  // levels[level] = files currently live at that level, sorted by file number.
  readonly levels: FileMetaData[][];
  nextFileNumber: number;
  readonly manifestFileNumber: number;
  private writer: ManifestWriter;

  private constructor(dir: string, manifestFileNumber: number) {
    this.dir = dir;
    this.levels = Array.from({ length: NUM_LEVELS }, () => []);
    this.nextFileNumber = 2; // 1 is reserved for the MANIFEST itself
    this.manifestFileNumber = manifestFileNumber;
    this.writer = new ManifestWriter(path.join(dir, manifestFileName(manifestFileNumber)));
  }

  get manifestPath(): string {
    return path.join(this.dir, manifestFileName(this.manifestFileNumber));
  }

  // Open (or create) the database directory and recover the latest version.
  static open(dir: string): VersionSet {
    fs.mkdirSync(dir, { recursive: true });
    const currentPath = path.join(dir, 'CURRENT');

    if (!fs.existsSync(currentPath)) {
      // Fresh database: create MANIFEST-000001 and point CURRENT at it.
      const vs = new VersionSet(dir, 1);
      const bootstrap = emptyEdit();
      bootstrap.nextFileNumber = vs.nextFileNumber;
      vs.writer.append(bootstrap);
      fs.writeFileSync(currentPath, manifestFileName(1) + '\n');
      fs.fsyncSync(fs.openSync(currentPath, 'r')); // ensure dir entry durability best-effort
      return vs;
    }

    const manifestName = fs.readFileSync(currentPath, 'utf8').trim();
    const manifestNumber = Number(manifestName.split('-')[1]);
    const vs = new VersionSet(dir, manifestNumber);

    const replay = replayManifest(vs.manifestPath);
    for (const edit of replay.edits) {
      vs.applyToMemory(edit);
    }
    if (replay.hadTornTail) {
      // Crash happened mid-append: drop the partial record so the log
      // ends exactly at the last committed edit.
      truncateToValid(vs.manifestPath, replay.validBytes);
    }
    return vs;
  }

  private applyToMemory(edit: VersionEdit): void {
    for (const [level, fileNumber] of edit.deletedFiles) {
      this.levels[level] = this.levels[level].filter((f) => f.number !== fileNumber);
    }
    for (const [level, meta] of edit.newFiles) {
      this.levels[level].push(meta);
      this.levels[level].sort((a, b) => a.number - b.number);
    }
    if (edit.nextFileNumber !== undefined) {
      this.nextFileNumber = edit.nextFileNumber;
    }
  }

  allocateFileNumber(): number {
    return this.nextFileNumber++;
  }

  // Atomically advance the version: persist the edit (append + fsync),
  // then mutate in-memory state. If the process dies before/during the
  // append, recovery replays only up to the previous committed edit.
  logAndApply(edit: VersionEdit): void {
    edit.nextFileNumber = this.nextFileNumber;
    this.writer.append(edit);
    this.applyToMemory(edit);
  }

  // All file numbers referenced by the current version.
  liveFiles(): Set<number> {
    const live = new Set<number>();
    for (const level of this.levels) {
      for (const f of level) live.add(f.number);
    }
    return live;
  }

  // Delete SSTables on disk that the current version does not reference.
  // These are orphans from compactions that crashed before committing.
  // Returns the removed file numbers.
  removeObsoleteFiles(): number[] {
    const live = this.liveFiles();
    const removed: number[] = [];
    for (const entry of fs.readdirSync(this.dir)) {
      const m = /^(\d{6})\.sst$/.exec(entry);
      if (!m) continue;
      const n = Number(m[1]);
      if (!live.has(n)) {
        fs.unlinkSync(path.join(this.dir, entry));
        removed.push(n);
      }
    }
    return removed.sort((a, b) => a - b);
  }

  // Files at a level that are no longer referenced after a successful
  // compaction commit (inputs that were deleted from the version).
  filesOnDisk(): number[] {
    return fs.readdirSync(this.dir)
      .map((e) => /^(\d{6})\.sst$/.exec(e))
      .filter((m): m is RegExpExecArray => m !== null)
      .map((m) => Number(m[1]))
      .sort((a, b) => a - b);
  }

  close(): void {
    this.writer.close();
  }
}
