import fs from "node:fs";
import path from "node:path";
import { atomicWriteFile, fsyncDir } from "./env.ts";
import { ManifestWriter, readManifest } from "./manifest.ts";
import { sstableFileName, verifySSTable } from "./sstable.ts";
import { VersionEdit, type FileMetaData } from "./version_edit.ts";

export const NUM_LEVELS = 7;
const CURRENT_FILE = "CURRENT";
const MANIFEST_PREFIX = "MANIFEST-";

export class VersionSet {
  readonly dbDir: string;
  levels: Map<number, FileMetaData>[];
  nextFileNumber = 2;
  manifestFileNumber = 1;
  private writer: ManifestWriter | null = null;

  private constructor(dbDir: string) {
    this.dbDir = dbDir;
    this.levels = Array.from({ length: NUM_LEVELS }, () => new Map());
  }

  static open(dbDir: string): VersionSet {
    fs.mkdirSync(dbDir, { recursive: true });
    const vs = new VersionSet(dbDir);
    vs.recover();
    return vs;
  }

  private recover(): void {
    const currentPath = path.join(this.dbDir, CURRENT_FILE);
    let manifestName: string;

    if (fs.existsSync(currentPath)) {
      manifestName = fs.readFileSync(currentPath, "utf8").trim();
    } else {
      manifestName = MANIFEST_PREFIX + String(this.manifestFileNumber).padStart(6, "0");
      atomicWriteFile(this.dbDir, CURRENT_FILE, manifestName + "\n");
    }

    const manifestPath = path.join(this.dbDir, manifestName);
    if (fs.existsSync(manifestPath)) {
      const { edits, validBytes, corruptedTail } = readManifest(manifestPath);
      for (const edit of edits) this.apply(edit);
      if (corruptedTail) fs.truncateSync(manifestPath, validBytes);
    }

    this.writer = new ManifestWriter(manifestPath, true);
    this.removeObsoleteFiles();
    this.verifyLiveFiles();
  }

  private apply(edit: VersionEdit): void {
    for (const del of edit.deletedFiles) {
      this.levels[del.level].delete(del.fileNumber);
    }
    for (const add of edit.addedFiles) {
      this.levels[add.level].set(add.meta.fileNumber, add.meta);
    }
    if (edit.nextFileNumber !== undefined) {
      this.nextFileNumber = Math.max(this.nextFileNumber, edit.nextFileNumber);
    }
    for (const level of this.levels) {
      for (const meta of level.values()) {
        this.nextFileNumber = Math.max(this.nextFileNumber, meta.fileNumber + 1);
      }
    }
  }

  logAndApply(edit: VersionEdit): void {
    edit.nextFileNumber = this.nextFileNumber;
    if (!this.writer) throw new Error("VersionSet is closed");
    this.writer.append(edit);
    this.writer.sync();
    this.apply(edit);
  }

  allocateFileNumber(): number {
    return this.nextFileNumber++;
  }

  liveFiles(): Set<number> {
    const live = new Set<number>();
    for (const level of this.levels) {
      for (const fileNumber of level.keys()) live.add(fileNumber);
    }
    return live;
  }

  filesAt(level: number): FileMetaData[] {
    return [...this.levels[level].values()].sort((a, b) => a.fileNumber - b.fileNumber);
  }

  removeObsoleteFiles(): string[] {
    const live = this.liveFiles();
    const removed: string[] = [];
    for (const name of fs.readdirSync(this.dbDir)) {
      if (!name.endsWith(".sst")) continue;
      const fileNumber = parseInt(name.slice(0, -4), 10);
      if (!live.has(fileNumber)) {
        fs.unlinkSync(path.join(this.dbDir, name));
        removed.push(name);
      }
    }
    if (removed.length > 0) fsyncDir(this.dbDir);
    return removed;
  }

  private verifyLiveFiles(): void {
    for (const level of this.levels) {
      for (const meta of level.values()) {
        if (!verifySSTable(this.dbDir, meta)) {
          throw new Error(
            `SSTable ${sstableFileName(meta.fileNumber)} failed checksum verification`,
          );
        }
      }
    }
  }

  close(): void {
    this.writer?.close();
    this.writer = null;
  }
}
