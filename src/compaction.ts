// L0 -> L1 compaction with explicit crash-injection points.
//
// Commit protocol (write-ahead for metadata):
//   1. Read all L0 inputs, merge-sort their entries.
//   2. Write the new L1 SSTable(s) to disk and fsync them.
//      <--- CRASH HERE: new files are orphans, version is untouched.
//   3. logAndApply(edit): append the VersionEdit to MANIFEST + fsync.
//      This single record atomically deletes L0 inputs and adds L1 outputs.
//      <--- CRASH HERE (mid-append): torn record is discarded on recovery.
//   4. Only now may the L0 input files be deleted from disk.

import * as path from 'node:path';
import { VersionSet, sstableFileName } from './versionSet.ts';
import { buildSSTable, readSSTable } from './sstable.ts';
import { emptyEdit, type FileMetaData, type VersionEdit } from './versionEdit.ts';

export interface CompactionResult {
  edit: VersionEdit;
  outputFiles: FileMetaData[];
  committed: boolean;
}

export interface CompactionOptions {
  // Simulate a crash after the new SSTables are durable but before the
  // MANIFEST commit. The edit is never appended.
  crashBeforeCommit?: boolean;
}

export function compactL0ToL1(vs: VersionSet, opts: CompactionOptions = {}): CompactionResult {
  const inputs = vs.levels[0];
  if (inputs.length === 0) throw new Error('nothing to compact: L0 is empty');

  // 1. Merge all L0 entries (later files win on duplicate keys).
  const merged = new Map<string, string>();
  for (const f of inputs) {
    for (const [k, v] of readSSTable(path.join(vs.dir, sstableFileName(f.number)))) {
      merged.set(k, v);
    }
  }

  // 2. Write the output SSTable and fsync it (still uncommitted).
  const outNumber = vs.allocateFileNumber();
  const outPath = path.join(vs.dir, sstableFileName(outNumber));
  const info = buildSSTable(outPath, [...merged.entries()]);
  const outputMeta: FileMetaData = {
    number: outNumber,
    fileSize: info.fileSize,
    smallest: info.smallest,
    largest: info.largest,
    checksum: info.checksum,
  };

  const edit = emptyEdit();
  for (const f of inputs) edit.deletedFiles.push([0, f.number]);
  edit.newFiles.push([1, outputMeta]);

  if (opts.crashBeforeCommit) {
    // Simulated crash: process dies here. The MANIFEST never saw the edit.
    return { edit, outputFiles: [outputMeta], committed: false };
  }

  // 3. Atomic commit.
  vs.logAndApply(edit);

  // 4. Post-commit cleanup of obsoleted L0 inputs.
  vs.removeObsoleteFiles();

  return { edit, outputFiles: [outputMeta], committed: true };
}
