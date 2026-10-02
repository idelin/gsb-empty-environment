import { readSSTable, writeSSTable, type Entry } from "./sstable.ts";
import { VersionEdit, type FileMetaData } from "./version_edit.ts";
import type { VersionSet } from "./version_set.ts";

export interface PreparedCompaction {
  edit: VersionEdit;
  outputs: FileMetaData[];
}

export function prepareL0ToL1Compaction(vs: VersionSet): PreparedCompaction | null {
  const l0Files = vs.filesAt(0);
  if (l0Files.length === 0) return null;

  const merged = new Map<string, string>();
  for (const meta of [...l0Files].reverse()) {
    for (const [key, value] of readSSTable(vs.dbDir, meta.fileNumber)) {
      if (!merged.has(key)) merged.set(key, value);
    }
  }
  const entries: Entry[] = [...merged.entries()];

  const outputNumber = vs.allocateFileNumber();
  const outputMeta = writeSSTable(vs.dbDir, outputNumber, entries);

  const edit = new VersionEdit();
  for (const meta of l0Files) edit.deleteFile(0, meta.fileNumber);
  edit.addFile(1, outputMeta);

  return { edit, outputs: [outputMeta] };
}

export function commitCompaction(vs: VersionSet, prepared: PreparedCompaction): void {
  vs.logAndApply(prepared.edit);
  vs.removeObsoleteFiles();
}
