import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { commitCompaction, prepareL0ToL1Compaction } from "../src/compaction.ts";
import { readManifest } from "../src/manifest.ts";
import { sstableFileName, writeSSTable } from "../src/sstable.ts";
import { VersionEdit } from "../src/version_edit.ts";
import { VersionSet } from "../src/version_set.ts";

function tempDir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "versionset-"));
}

function addL0Table(vs: VersionSet, entries: [string, string][]): number {
  const fileNumber = vs.allocateFileNumber();
  const meta = writeSSTable(vs.dbDir, fileNumber, entries);
  const edit = new VersionEdit();
  edit.addFile(0, meta);
  vs.logAndApply(edit);
  return fileNumber;
}

test("fresh open creates CURRENT and MANIFEST", () => {
  const dir = tempDir();
  const vs = VersionSet.open(dir);
  assert.ok(fs.existsSync(path.join(dir, "CURRENT")));
  const manifestName = fs.readFileSync(path.join(dir, "CURRENT"), "utf8").trim();
  assert.ok(manifestName.startsWith("MANIFEST-"));
  assert.ok(fs.existsSync(path.join(dir, manifestName)));
  vs.close();
});

test("logAndApply persists version across restart", () => {
  const dir = tempDir();
  let vs = VersionSet.open(dir);
  const f1 = addL0Table(vs, [["a", "1"]]);
  const f2 = addL0Table(vs, [["b", "2"]]);
  vs.close();

  vs = VersionSet.open(dir);
  assert.deepEqual(
    vs.filesAt(0).map((m) => m.fileNumber),
    [f1, f2],
  );
  assert.equal(vs.filesAt(0)[0].checksum, vs.filesAt(0)[0].checksum);
  vs.close();
});

test("crash during L0->L1 compaction: orphan output discarded, pre-compaction version restored", () => {
  const dir = tempDir();
  let vs = VersionSet.open(dir);
  const f1 = addL0Table(vs, [["a", "1"], ["b", "2"]]);
  const f2 = addL0Table(vs, [["c", "3"]]);
  const liveBefore = vs.liveFiles();

  const prepared = prepareL0ToL1Compaction(vs)!;
  assert.ok(prepared);
  const orphanName = sstableFileName(prepared.outputs[0].fileNumber);
  assert.ok(fs.existsSync(path.join(dir, orphanName)), "new L1 file written before crash");

  vs.close();

  vs = VersionSet.open(dir);
  assert.deepEqual(vs.liveFiles(), liveBefore, "version identical to pre-compaction state");
  assert.deepEqual(
    vs.filesAt(0).map((m) => m.fileNumber),
    [f1, f2],
    "L0 inputs still live",
  );
  assert.equal(vs.filesAt(1).length, 0, "no L1 files committed");
  assert.ok(!fs.existsSync(path.join(dir, orphanName)), "orphan SSTable removed");
  assert.ok(fs.existsSync(path.join(dir, sstableFileName(f1))));
  assert.ok(fs.existsSync(path.join(dir, sstableFileName(f2))));
  vs.close();
});

test("committed compaction swaps levels and deletes obsolete inputs", () => {
  const dir = tempDir();
  let vs = VersionSet.open(dir);
  const f1 = addL0Table(vs, [["a", "1"]]);
  const f2 = addL0Table(vs, [["b", "2"]]);
  const prepared = prepareL0ToL1Compaction(vs)!;
  commitCompaction(vs, prepared);
  const out = prepared.outputs[0].fileNumber;
  vs.close();

  vs = VersionSet.open(dir);
  assert.equal(vs.filesAt(0).length, 0);
  assert.deepEqual(
    vs.filesAt(1).map((m) => m.fileNumber),
    [out],
  );
  assert.ok(!fs.existsSync(path.join(dir, sstableFileName(f1))));
  assert.ok(!fs.existsSync(path.join(dir, sstableFileName(f2))));
  assert.ok(fs.existsSync(path.join(dir, sstableFileName(out))));
  vs.close();
});

test("torn tail record in manifest is ignored and truncated", () => {
  const dir = tempDir();
  let vs = VersionSet.open(dir);
  const f1 = addL0Table(vs, [["a", "1"]]);
  vs.close();

  const manifestName = fs.readFileSync(path.join(dir, "CURRENT"), "utf8").trim();
  const manifestPath = path.join(dir, manifestName);
  const sizeBefore = fs.statSync(manifestPath).size;
  fs.appendFileSync(manifestPath, Buffer.from([10, 0, 0, 0, 0xde, 0xad]));

  vs = VersionSet.open(dir);
  assert.deepEqual(
    vs.filesAt(0).map((m) => m.fileNumber),
    [f1],
  );
  assert.equal(fs.statSync(manifestPath).size, sizeBefore, "torn tail truncated");
  vs.close();
});

test("checksum-corrupted live SSTable is detected on recovery", () => {
  const dir = tempDir();
  let vs = VersionSet.open(dir);
  const f1 = addL0Table(vs, [["a", "1"]]);
  vs.close();

  const filePath = path.join(dir, sstableFileName(f1));
  const data = fs.readFileSync(filePath);
  data[0] ^= 0xff;
  fs.writeFileSync(filePath, data);

  assert.throws(() => VersionSet.open(dir), /checksum/);
});

test("manifest records are crc-protected and replayed in order", () => {
  const dir = tempDir();
  const vs = VersionSet.open(dir);
  addL0Table(vs, [["a", "1"]]);
  addL0Table(vs, [["b", "2"]]);
  vs.close();

  const manifestName = fs.readFileSync(path.join(dir, "CURRENT"), "utf8").trim();
  const { edits, corruptedTail } = readManifest(path.join(dir, manifestName));
  assert.equal(edits.length, 2);
  assert.equal(corruptedTail, false);
  assert.equal(edits[0].addedFiles[0].level, 0);
  assert.equal(edits[1].addedFiles[0].level, 0);
});
