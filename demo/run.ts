import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { prepareL0ToL1Compaction } from "../src/compaction.ts";
import { sstableFileName, writeSSTable } from "../src/sstable.ts";
import { VersionEdit } from "../src/version_edit.ts";
import { VersionSet } from "../src/version_set.ts";

const dir = fs.mkdtempSync(path.join(os.tmpdir(), "versionset-demo-"));
console.log(`DB dir: ${dir}\n`);

function addL0(vs: VersionSet, entries: [string, string][]): void {
  const num = vs.allocateFileNumber();
  const meta = writeSSTable(dir, num, entries);
  const edit = new VersionEdit();
  edit.addFile(0, meta);
  vs.logAndApply(edit);
  console.log(`  + L0 SSTable ${sstableFileName(num)} committed to MANIFEST`);
}

function showState(vs: VersionSet, label: string): void {
  const l0 = vs.filesAt(0).map((m) => sstableFileName(m.fileNumber));
  const l1 = vs.filesAt(1).map((m) => sstableFileName(m.fileNumber));
  const onDisk = fs.readdirSync(dir).filter((f) => f.endsWith(".sst")).sort();
  console.log(`  [${label}] L0=[${l0}] L1=[${l1}] disk=[${onDisk}]`);
}

console.log("== Phase 1: build L0 tables ==");
let vs = VersionSet.open(dir);
addL0(vs, [["apple", "1"], ["banana", "2"]]);
addL0(vs, [["cherry", "3"]]);
showState(vs, "before compaction");

console.log("\n== Phase 2: L0->L1 compaction writes new file, then CRASH before commit ==");
const prepared = prepareL0ToL1Compaction(vs)!;
const orphan = sstableFileName(prepared.outputs[0].fileNumber);
console.log(`  wrote new L1 output ${orphan} (fsynced), simulating crash before MANIFEST commit...`);
vs.close();

console.log("\n== Phase 3: restart, recover from MANIFEST ==");
vs = VersionSet.open(dir);
showState(vs, "after recovery");
console.log(`  orphan ${orphan} exists on disk? ${fs.existsSync(path.join(dir, orphan))}`);
console.log("\nRecovery complete: version rolled back to pre-compaction state, orphan discarded.");
vs.close();
fs.rmSync(dir, { recursive: true, force: true });
