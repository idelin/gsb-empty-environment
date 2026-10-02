import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Engine } from "../src/engine.ts";

const dir = mkdtempSync(join(tmpdir(), "lsm-demo-"));
console.log(`data dir: ${dir}\n`);

// 1. One LSM instance, two isolated column families
let db = Engine.open(dir);
db.createColumnFamily("CF_Default");
db.createColumnFamily("CF_Metadata");
console.log("column families:", db.listColumnFamilies());

// 2. One atomic transaction writing into BOTH families
db.writeBatch([
  { cf: "CF_Default", key: "user:1001", value: '{"name":"alice"}' },
  { cf: "CF_Metadata", key: "table:users:version", value: "7" },
]);
console.log("\n[atomic batch committed across CF_Default + CF_Metadata]");

// 3. Isolation: CF_Metadata only sees its own data
console.log("CF_Metadata keys:", db.keys("CF_Metadata"));
console.log("CF_Metadata get user:1001 ->", db.get("CF_Metadata", "user:1001"));
console.log("CF_Default  get user:1001 ->", db.get("CF_Default", "user:1001"));

// 4. Independent snapshot of a single CF
const snap = db.snapshot("CF_Default");
db.put("CF_Default", "user:1001", '{"name":"alice","age":30}');
console.log("\nsnapshot sees user:1001 ->", snap.get("user:1001"));
console.log("live read sees user:1001 ->", db.get("CF_Default", "user:1001"));

// 5. Flush one CF independently, then crash-recover
db.flush("CF_Default");
db.close();
console.log("\n[reopening instance: replay MANIFEST + shared WAL]");
db = Engine.open(dir);
console.log("CF_Default  user:1001 ->", db.get("CF_Default", "user:1001"));
console.log("CF_Metadata version   ->", db.get("CF_Metadata", "table:users:version"));
console.log("both families aligned after recovery:",
  db.get("CF_Default", "user:1001") !== undefined &&
  db.get("CF_Metadata", "table:users:version") !== undefined);

db.close();
rmSync(dir, { recursive: true, force: true });
