// End-to-end demo of the acceptance scenario:
//   crash at the exact moment an L0->L1 compaction has written its new
//   SSTable but has not yet committed the VersionEdit; restart; verify
//   the orphan is discarded and the pre-compaction version is restored.

import * as fs from 'node:fs';
import * as path from 'node:path';
import * as os from 'node:os';
import { VersionSet, sstableFileName } from '../src/versionSet.ts';
import { buildSSTable, readSSTable } from '../src/sstable.ts';
import { compactL0ToL1 } from '../src/compaction.ts';
import { emptyEdit, type FileMetaData } from '../src/versionEdit.ts';

function flush(vs: VersionSet, entries: Array<[string, string]>): FileMetaData {
  const n = vs.allocateFileNumber();
  const info = buildSSTable(path.join(vs.dir, sstableFileName(n)), entries);
  const meta: FileMetaData = { number: n, ...info };
  const edit = emptyEdit();
  edit.newFiles.push([0, meta]);
  vs.logAndApply(edit);
  console.log(`  flushed L0 table ${sstableFileName(n)} (${entries.length} keys, crc=${meta.checksum.toString(16)})`);
  return meta;
}

function showState(tag: string, vs: VersionSet): void {
  const lv = vs.levels.map((l, i) => (l.length ? `L${i}=[${l.map((f) => sstableFileName(f.number)).join(',')}]` : null))
    .filter(Boolean).join(' ');
  console.log(`  [${tag}] levels: ${lv || '(empty)'}  on-disk: ${vs.filesOnDisk().map(sstableFileName).join(', ') || '(none)'}`);
}

const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'vs-demo-'));
console.log(`DB dir: ${dir}\n`);

console.log('1. Open DB, flush two memtables to L0');
let vs = VersionSet.open(dir);
flush(vs, [['apple', '1'], ['banana', '2']]);
flush(vs, [['banana', '20'], ['cherry', '3']]);
showState('before compaction', vs);
vs.close();

console.log('\n2. Start L0->L1 compaction; CRASH right after the new SSTable is durable, before MANIFEST commit');
vs = VersionSet.open(dir);
const crashed = compactL0ToL1(vs, { crashBeforeCommit: true });
console.log(`  wrote orphan output ${sstableFileName(crashed.outputFiles[0].number)} -- process dies now (no commit)`);
vs.close();

console.log('\n3. Restart: replay MANIFEST, recover latest committed version');
vs = VersionSet.open(dir);
showState('after recovery', vs);

console.log('\n4. Discard orphan SSTables not referenced by the version');
const removed = vs.removeObsoleteFiles();
console.log(`  removed orphans: ${removed.map(sstableFileName).join(', ') || '(none)'}`);
showState('after cleanup', vs);

console.log('\n5. Live data is intact and readable');
for (const f of vs.levels[0]) {
  console.log(`  ${sstableFileName(f.number)}: ${JSON.stringify(readSSTable(path.join(dir, sstableFileName(f.number))))}`);
}

console.log('\n6. Re-run the compaction to completion (atomic commit), restart, verify');
const done = compactL0ToL1(vs);
console.log(`  committed edit: delete L0 ${done.edit.deletedFiles.map((d) => sstableFileName(d[1])).join(',')}, add L1 ${sstableFileName(done.outputFiles[0].number)}`);
vs.close();
vs = VersionSet.open(dir);
showState('after committed compaction + restart', vs);
console.log(`  L1 data: ${JSON.stringify(readSSTable(path.join(vs.dir, sstableFileName(vs.levels[1][0].number))))}`);
vs.close();

console.log('\nDemo finished OK');
