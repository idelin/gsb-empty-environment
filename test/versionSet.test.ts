import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as path from 'node:path';
import * as os from 'node:os';
import { VersionSet, sstableFileName } from '../src/versionSet.ts';
import { buildSSTable, readSSTable, verifySSTable } from '../src/sstable.ts';
import { compactL0ToL1 } from '../src/compaction.ts';
import { emptyEdit, type FileMetaData } from '../src/versionEdit.ts';

function tmpdir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'vs-test-'));
}

// Simulate a memtable flush: write an SSTable and commit it to L0.
function flushToL0(vs: VersionSet, entries: Array<[string, string]>): FileMetaData {
  const n = vs.allocateFileNumber();
  const info = buildSSTable(path.join(vs.dir, sstableFileName(n)), entries);
  const meta: FileMetaData = {
    number: n, fileSize: info.fileSize,
    smallest: info.smallest, largest: info.largest, checksum: info.checksum,
  };
  const edit = emptyEdit();
  edit.newFiles.push([0, meta]);
  vs.logAndApply(edit);
  return meta;
}

test('fresh open creates MANIFEST and CURRENT with empty levels', () => {
  const dir = tmpdir();
  const vs = VersionSet.open(dir);
  assert.ok(fs.existsSync(path.join(dir, 'CURRENT')));
  assert.ok(fs.existsSync(path.join(dir, 'MANIFEST-000001')));
  assert.deepEqual(vs.liveFiles().size, 0);
  vs.close();
});

test('committed edits survive a restart', () => {
  const dir = tmpdir();
  let vs = VersionSet.open(dir);
  const a = flushToL0(vs, [['a', '1'], ['b', '2']]);
  const b = flushToL0(vs, [['c', '3']]);
  vs.close();

  vs = VersionSet.open(dir);
  assert.deepEqual(vs.levels[0].map((f) => f.number), [a.number, b.number]);
  assert.equal(vs.levels[0][0].checksum, a.checksum);
  assert.ok(verifySSTable(path.join(dir, sstableFileName(a.number)), a.checksum));
  vs.close();
});

test('crash during L0->L1 compaction output write: orphan discarded, pre-compaction version restored', () => {
  const dir = tmpdir();
  let vs = VersionSet.open(dir);
  const f1 = flushToL0(vs, [['apple', '1'], ['banana', '2']]);
  const f2 = flushToL0(vs, [['banana', '20'], ['cherry', '3']]);
  const nextBefore = vs.nextFileNumber;
  vs.close(); // simulate process exit boundary before compaction starts

  // Compaction starts, writes the new L1 SSTable, then "crashes"
  // before the VersionEdit is appended to the MANIFEST.
  vs = VersionSet.open(dir);
  const result = compactL0ToL1(vs, { crashBeforeCommit: true });
  assert.equal(result.committed, false);
  const orphan = result.outputFiles[0].number;
  assert.ok(fs.existsSync(path.join(dir, sstableFileName(orphan))), 'orphan exists at crash time');
  vs.close();

  // --- restart ---
  vs = VersionSet.open(dir);

  // Version is exactly the pre-compaction state: two L0 files, empty L1.
  assert.deepEqual(vs.levels[0].map((f) => f.number), [f1.number, f2.number]);
  assert.equal(vs.levels[1].length, 0);
  assert.equal(vs.nextFileNumber, nextBefore); // orphan number was never committed

  // Orphan is not referenced and gets garbage-collected.
  const removed = vs.removeObsoleteFiles();
  assert.deepEqual(removed, [orphan]);
  assert.ok(!fs.existsSync(path.join(dir, sstableFileName(orphan))));

  // Data of the live version is fully intact.
  const entries = [
    ...readSSTable(path.join(dir, sstableFileName(f1.number))),
    ...readSSTable(path.join(dir, sstableFileName(f2.number))),
  ];
  assert.deepEqual(entries, [
    ['apple', '1'], ['banana', '2'], ['banana', '20'], ['cherry', '3'],
  ]);
  vs.close();
});

test('torn MANIFEST tail (crash mid-append) is discarded on recovery', () => {
  const dir = tmpdir();
  let vs = VersionSet.open(dir);
  const f1 = flushToL0(vs, [['k', 'v']]);
  vs.close();

  // Simulate a crash while appending the next edit: partial record bytes.
  fs.appendFileSync(path.join(dir, 'MANIFEST-000001'), Buffer.from([0x30, 0x00, 0x00]));

  vs = VersionSet.open(dir);
  assert.deepEqual(vs.levels[0].map((f) => f.number), [f1.number]);

  // Torn tail was truncated; new commits still work after recovery.
  const f2 = flushToL0(vs, [['k2', 'v2']]);
  vs.close();
  vs = VersionSet.open(dir);
  assert.deepEqual(vs.levels[0].map((f) => f.number), [f1.number, f2.number]);
  vs.close();
});

test('successful compaction atomically swaps L0 for L1 and deletes inputs', () => {
  const dir = tmpdir();
  let vs = VersionSet.open(dir);
  const f1 = flushToL0(vs, [['a', '1'], ['b', '2']]);
  const f2 = flushToL0(vs, [['b', '22'], ['c', '3']]);

  const result = compactL0ToL1(vs);
  assert.equal(result.committed, true);
  assert.equal(vs.levels[0].length, 0);
  assert.equal(vs.levels[1].length, 1);
  const out = result.outputFiles[0];

  // Obsoleted L0 inputs are gone from disk; only the L1 output remains.
  assert.deepEqual(vs.filesOnDisk(), [out.number]);
  assert.ok(!fs.existsSync(path.join(dir, sstableFileName(f1.number))));
  assert.ok(!fs.existsSync(path.join(dir, sstableFileName(f2.number))));

  // Merged data: later L0 file wins on duplicate key.
  assert.deepEqual(readSSTable(path.join(dir, sstableFileName(out.number))), [
    ['a', '1'], ['b', '22'], ['c', '3'],
  ]);
  vs.close();

  // Restart: the compacted version is recovered exactly.
  vs = VersionSet.open(dir);
  assert.equal(vs.levels[0].length, 0);
  assert.deepEqual(vs.levels[1].map((f) => f.number), [out.number]);
  assert.ok(verifySSTable(path.join(dir, sstableFileName(out.number)), out.checksum));
  vs.close();
});

test('corrupted SSTable is detected by checksum', () => {
  const dir = tmpdir();
  const p = path.join(dir, 'corrupt.sst');
  const info = buildSSTable(p, [['x', 'y']]);
  assert.ok(verifySSTable(p, info.checksum));
  const buf = fs.readFileSync(p);
  buf[buf.length - 6] ^= 0xff; // flip a payload byte
  fs.writeFileSync(p, buf);
  assert.equal(verifySSTable(p, info.checksum), false);
  assert.throws(() => readSSTable(p), /checksum mismatch/);
});
